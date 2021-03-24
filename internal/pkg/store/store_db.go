package store

import (
	"fmt"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log"
	"os"
	"sync"
	"time"
)

type gorm_store struct {
	serverId       string
	serverHostPort string
	db             *gorm.DB
	log            *log.Logger
	stopChan       chan struct{}
	wg             sync.WaitGroup
}

func NewDBStore(dsn string, serverHostPort string) (Store, error) {
	db, err := gorm.Open(postgres.Open(dsn))
	if err != nil {
		return nil, err
	}
	return &gorm_store{
		serverHostPort: serverHostPort,
		serverId:       ksuid.New().String(),
		db:             db,
		log:            log.New(os.Stdout, "store: ", 0),
		stopChan:       make(chan struct{}),
	}, nil
}

func (store *gorm_store) OnListen(service string, token string, from string) (redirect string, close func(), err error) {
	type Result struct {
		Allowed bool
	}
	var result Result

	// Are they allowed to connect?
	err = store.db.Raw("SELECT allowed_to_listen @> ? AS allowed FROM services WHERE id = ?", []string{token}, service).
		Scan(&result).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil, ServiceNotFound
		}
		return "", nil, err
	}

	if !result.Allowed {
		return "", nil, PermissionDenied
	}

	// Record the binding so that other servers can redirect traffic to us.
	bindingId := ksuid.New().String()
	err = store.db.Create(Binding{
		ID:             bindingId,
		ServiceID:      service,
		ServerHostPort: store.serverHostPort,
		ServerID:       store.serverId,
		From:           from,
	}).Error
	if err != nil {
		return "", nil, err
	}

	return "", func() {
		store.db.Delete(Binding{ID: bindingId})
	}, nil
}

func (store *gorm_store) OnConnect(service string, token string, from string) (redirect string, close func(), err error) {
	type Result struct {
		Allowed bool
	}
	var result Result
	err = store.db.Raw("SELECT allowed_to_connect @> ? AS allowed FROM services WHERE id = ?", []string{token}, service).
		Scan(&result).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil, ServiceNotFound
		}
		return "", nil, err
	}

	if !result.Allowed {
		return "", nil, PermissionDenied
	}

	// Do this server have a binding?
	var count int64
	err = store.db.Where(Binding{ServerID: store.serverId, ServiceID: service}).Count(&count).Error
	if err != nil {
		return "", nil, err
	}
	if count > 0 {
		return "", nil, nil
	}

	// Lets find a server to redirect to:
	var b Binding
	err = store.db.Order("created_at desc").Limit(1).First(&b).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil, ServiceNotFound
		}
		return "", nil, err
	}
	return b.ServerHostPort, nil, nil
}

type Service struct {
	ID               string   `gorm:"primaryKey"`
	AllowedToListen  []string `gorm:"type:text[]"`
	AllowedToConnect []string `gorm:"type:text[]"`
	Owner            string
}

type Server struct {
	ID        string    `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"default:now()"`
	AliveAt   time.Time `gorm:"default:now()"`
}

type Binding struct {
	ID             string `gorm:"primaryKey"`
	ServiceID      string `gorm:"index:"`
	ServerHostPort string
	ServerID       string
	CreatedAt      time.Time `gorm:"default:now()"`
	From           string
}

func (store *gorm_store) Stop() {
	close(store.stopChan)
	store.wg.Wait()

	store.db.Delete(Server{ID: store.serverId})
	store.db.Where("server_id=?", store.serverId).Delete(Binding{})
}

func (store *gorm_store) Start() error {
	store.stopChan = make(chan struct{}, 1)

	migrator := store.db.Migrator()

	err := migrator.AutoMigrate(&Server{})
	if err != nil {
		return err
	}

	err = migrator.AutoMigrate(&Binding{})
	if err != nil {
		return err
	}

	err = migrator.AutoMigrate(&Service{})
	if err != nil {
		return err
	}

	err = store.db.Create(&Server{
		ID: store.serverId,
	}).Error
	if err != nil {
		return err
	}

	store.wg.Add(1)
	go func() {
		defer store.wg.Done()
		store.backgroundTasks()
	}()
	return nil
}

func (store *gorm_store) backgroundTasks() {
	for {
		select {
		case <-store.stopChan:
			return
		case <-time.After(10 * time.Second):

			err := store.db.Model(&Server{
				ID: store.serverId,
			}).Save(map[string]interface{}{
				"AliveAt": clause.Expr{SQL: "now()"},
			}).Error

			if err != nil {
				panic(fmt.Sprintf("could not update server db record: %v", err))
			}

			var deadServers []Server
			err = store.db.Where("(AliveAt + INTERVAL '20 seconds') < now()").Find(&deadServers).Error
			if err != nil {
				panic(fmt.Sprintf("could not look for dead servers: %v", err))
			}

			for _, s := range deadServers {
				// Delete the server record and all it's bindings..
				store.db.Delete(s)
				store.db.Where("server_id=?", s.ID).Delete(Binding{})
			}
		}
	}

}