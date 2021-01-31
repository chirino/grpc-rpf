package importer

import (
	"io/ioutil"
	"log"
	"net"
	"path/filepath"
	"sort"

	"github.com/chirino/rtsvc/internal/cmd/app"
	"github.com/chirino/rtsvc/internal/pkg/importer"
	"github.com/chirino/rtsvc/internal/pkg/utils"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

var (
	listen     = ":0"
	importsDir = "imports.d"
	config     = importer.Config{}
	Command    = &cobra.Command{
		Use:   "importer",
		Short: "Runs the importer",
		Run:   commandRun,
	}
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "address to ")
	Command.Flags().StringVar(&importsDir, "imports-dir", importsDir, "directory holding import configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "cafile", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "cafile", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "keyfile", "importer.key", "private key file")
	//_ = Command.MarkFlagRequired("config")
	app.Command.AddCommand(Command)
}

type ImportConfig struct {
	Name   string
	Listen string
}

func commandRun(cmd *cobra.Command, args []string) {

	app.HandleErrorWithExitCode(func() error {
		importerListener, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}

		s, err := importer.New(config)
		if err != nil {
			return err
		}
		s.Serve(importerListener)

		stopWatching, err := utils.WatchDir(importsDir, loadImporterConfigs)
		if err != nil {
			return err
		}
		defer stopWatching()


		////log.Printf("stopping importer")
		//s.Stop()
		return nil
	}, 2)
}

func loadImporterConfigs() {
	files, err := ioutil.ReadDir(importsDir)
	if err != nil {
		log.Printf("error loading imports directory: %v", err)
		return
	}

	var values []ImportConfig
	for _, f := range files {
		value := ImportConfig{}
		file := filepath.Join(importsDir, f.Name())
		err := utils.ReadConfigFile(file, &value)
		if err != nil {
			log.Printf("error loading import file '%s': %v", file, err)
			continue
		}
		values = append(values, value)
	}
}
