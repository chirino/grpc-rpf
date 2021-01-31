package utils

import (
	"os"

	"gopkg.in/yaml.v2"
)

func ReadConfigFile(file string, x interface{}) error {
	r, err := os.Open(file)
	if err != nil {
		return err
	}
	defer r.Close()
	return yaml.NewDecoder(r).Decode(x)
}
