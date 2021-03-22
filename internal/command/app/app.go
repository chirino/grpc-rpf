package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"os"
	"strings"
)

var (
	Command = &cobra.Command{
		Use:               "grpc-rpf",
		Short:             "GRPC Remote Port Forward",
		PersistentPreRunE: BindEnv("GRPC_RPF"),
	}
	DevMode = false
)

func init() {
	Command.PersistentFlags().BoolVar(&DevMode, "dev-mode", false, "run in development mode. increases output verbosity.")
}

func HandleErrorWithExitCode(function func() error, code int) {
	if err := function(); err != nil {
		if DevMode {
			fmt.Printf("%+v", err)
		} else {
			fmt.Printf("%v", err)
		}
		os.Exit(code)
	}
}

func BindEnv(envPrefix string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		v := viper.New()
		v.SetEnvPrefix(envPrefix)
		v.AutomaticEnv()

		cmd.Flags().VisitAll(func(f *pflag.Flag) {

			// Environment variables can't have dashes in them, so bind them to their equivalent
			// keys with underscores, e.g. --favorite-color to FAVORITE_COLOR
			if strings.Contains(f.Name, "-") {
				envVarSuffix := strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
				_ = v.BindEnv(f.Name, fmt.Sprintf("%s_%s", envPrefix, envVarSuffix))
			}

			// Apply the viper config value to the flag when the flag is not set and viper has a value
			if !f.Changed && v.IsSet(f.Name) {
				val := v.Get(f.Name)
				_ = cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
			}
		})
		return nil
	}
}
