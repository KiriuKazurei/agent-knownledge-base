package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "KnowledgeAgentHub"

func Set(id, value string) error {
	if value == "" {
		return nil
	}
	return keyring.Set(service, id, value)
}

func Get(id string) (string, error) {
	value, err := keyring.Get(service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return value, err
}

func Delete(id string) error {
	err := keyring.Delete(service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
