package tempushttprpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"tempus-completion/tempushttp"
)

type Client struct {
	address string
	httpc   http.Client
}

func NewClient(httpc http.Client, address string) *Client {
	if address == "" {
		address = "https://tempus2.xyz/api/v0"
	}

	return &Client{
		address: address,
		httpc:   httpc,
	}
}

func (c *Client) SearchPlayersAndMaps(ctx context.Context, name string) (*tempushttp.PlayersAndMapsSearchResponse, error) {
	addr := c.address + "/search/playersAndMaps/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	res, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, string(b))
	}

	var response tempushttp.PlayersAndMapsSearchResponse

	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &response, nil
}

func (c *Client) GetDetailedMapList(ctx context.Context) (tempushttp.GetDetailedMapListResponse, error) {
	addr := c.address + "/maps/detailedList"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	res, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, string(b))
	}

	var response tempushttp.GetDetailedMapListResponse

	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return response, nil
}

func (c *Client) GetZoneRecords(ctx context.Context, mapID uint64, zoneType tempushttp.ZoneType, zoneIndex uint8, limit uint32) (*tempushttp.ZoneRecordsResponse, error) {
	addr := fmt.Sprintf("%s/maps/id/%d/zones/typeindex/%s/%d/records/list?limit=%d", c.address, mapID, zoneType, zoneIndex, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	res, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, string(b))
	}

	var response tempushttp.ZoneRecordsResponse

	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &response, nil
}

type GetPlayerZoneClassCompletionData struct {
	MapName   string
	ZoneType  tempushttp.ZoneType
	ZoneIndex uint8
	PlayerID  uint64
	Class     tempushttp.ClassType
}

func (c *Client) GetPlayerZoneClassCompletion(ctx context.Context, data GetPlayerZoneClassCompletionData) (*tempushttp.GetPlayerZoneClassCompletionResponse, error) {
	addr := fmt.Sprintf("%s/maps/name/%s/zones/typeindex/%s/%d/records/player/%d/%d", c.address, data.MapName, data.ZoneType, data.ZoneIndex, data.PlayerID, data.Class)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	res, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, string(b))
	}

	var response tempushttp.GetPlayerZoneClassCompletionResponse

	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &response, nil
}
