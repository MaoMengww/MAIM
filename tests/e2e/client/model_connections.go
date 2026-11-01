package main

import (
	"errors"
	"net/http"
)

func (d *driver) fixtureConnection(owner account, name, provider, baseURL, key string) (entityID, error) {
	var connection struct {
		ID entityID `json:"id"`
	}
	if err := d.request(http.MethodPost, "/model-connections", owner.token, map[string]any{"name": name, "provider": provider, "base_url": baseURL, "api_key": key}, &connection); err != nil {
		return "", err
	}
	if connection.ID == "" {
		return "", errors.New("fixture connection missing identity")
	}
	return connection.ID, nil
}

func (d *driver) updateFixtureConnection(owner account, modelID entityID, fields map[string]any) error {
	var models struct {
		Items []struct {
			ID           entityID `json:"id"`
			ConnectionID entityID `json:"connection_id"`
		} `json:"items"`
	}
	if err := d.request(http.MethodGet, "/models", owner.token, nil, &models); err != nil {
		return err
	}
	for _, model := range models.Items {
		if model.ID == modelID {
			return d.request(http.MethodPut, "/model-connections/"+model.ConnectionID.String(), owner.token, fields, nil)
		}
	}
	return errors.New("fixture model connection unavailable")
}
