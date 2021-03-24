package store

import (
	"fmt"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	"io/ioutil"
	"log"
	"path/filepath"
	"sync"
)

type dir_store struct {
	dir          string
	mu           sync.RWMutex
	log          log.Logger
	services     map[string]ImportConfig
	stopWatching func() error
}

func (store *dir_store) OnListen(service string, token string, from string) (redirect string, close func(), err error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	s, found := store.services[service]
	if !found {
		return "", nil, ServiceNotFound
	}
	if len(s.AllowedListenTokens) > 0 {
		for _, allowedToken := range s.AllowedListenTokens {
			if token == allowedToken {
				return "", nil, nil
			}
		}
		return "", nil, PermissionDenied
	}
	return "", nil, nil
}

func (store *dir_store) OnConnect(service string, token string, from string) (redirect string, close func(), err error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	s, found := store.services[service]
	if !found {
		return "", nil, ServiceNotFound
	}
	if len(s.AllowedConnectTokens) > 0 {
		for _, allowedToken := range s.AllowedConnectTokens {
			if token == allowedToken {
				return "", nil, nil
			}
		}
		return "", nil, PermissionDenied
	}
	return "", nil, nil
}

type ImportConfig struct {
	Name                 string   `yaml:"name"`
	Listen               string   `yaml:"listen"`
	AllowedListenTokens  []string `yaml:"allowed-listen-tokens"`
	AllowedConnectTokens []string `yaml:"allowed-connect-tokens"`
}

func NewDirStore(dsn string) (Store, error) {
	return &dir_store{}, nil
}

func (store *dir_store) Stop() {
	if store.stopWatching != nil {
		_ = store.stopWatching()
	}
}

func (store *dir_store) Start() error {

	err := store.loadImporterConfigs()
	if err != nil {
		return err
	}
	store.stopWatching, err = utils.WatchDir(store.dir, func() {
		store.log.Println("config change detected")
		err := store.loadImporterConfigs()
		if err != nil {
			store.log.Println(err)
		}
	})
	if err != nil {
		return err
	}

	return nil
}

func (store *dir_store) loadImporterConfigs() error {
	files, err := ioutil.ReadDir(store.dir)
	if err != nil {
		return fmt.Errorf("error loading imports directory: %v", err)
	}

	im := map[string]ImportConfig{}
	for _, f := range files {

		value := ImportConfig{}
		file := filepath.Join(store.dir, f.Name())
		err := utils.ReadConfigFile(file, &value)
		if err != nil {
			store.log.Printf("error loading import file '%s': %v", file, err)
			continue
		}

		if value.Name == "" {
			store.log.Printf("error loading import file '%s': service name not configured", file)
			continue
		}

		if value.Listen == "" {
			store.log.Printf("error loading import file '%s': service listen address not configured", file)
			continue
		}
		im[value.Name] = value

	}
	store.mu.Lock()
	store.services = im
	store.mu.Unlock()
	return nil
}
