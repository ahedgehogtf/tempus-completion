package tempushttprpc_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"tempus-completion/tempushttp"
	"tempus-completion/tempushttprpc"
	"testing"
	"time"
)

func TestClientGetDetailedMapList(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "detailed-map-list.json"))
	if err != nil {
		t.Fatalf("read detailed map-list.json: %s", err)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, bytes.NewReader(b))
	}

	ts := httptest.NewServer(http.HandlerFunc(handler))

	defer ts.Close()

	httpc := http.Client{}
	c := tempushttprpc.NewClient(httpc, ts.URL)

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)

	defer cancel()

	response, err := c.GetDetailedMapList(ctx)
	if err != nil {
		t.Fatalf("request error: %s", err)
	}

	if len(response) == 0 {
		t.Fatalf("did not receive any data")
	}

	fmt.Println(response)
}

func TestClientGetPlayerZoneClassCompletionCompleted(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "player-zone-class-completion-completed.json"))
	if err != nil {
		t.Fatalf("read json: %s", err)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, bytes.NewReader(b))
	}

	ts := httptest.NewServer(http.HandlerFunc(handler))

	defer ts.Close()

	httpc := http.Client{}
	c := tempushttprpc.NewClient(httpc, ts.URL)

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)

	defer cancel()

	data := tempushttprpc.GetPlayerZoneClassCompletionData{
		MapName:   "jump_cow",
		ZoneType:  tempushttp.ZoneTypeMap,
		ZoneIndex: 1,
		PlayerID:  59983,
		Class:     tempushttp.ClassTypeSoldier,
	}

	response, err := c.GetPlayerZoneClassCompletion(ctx, data)
	if err != nil {
		t.Fatalf("request error: %s", err)
	}

	if response.Result.ID != 6800470 {
		t.Fatalf("response malformed")
	}
}

func TestClientGetPlayerZoneClassCompletionIncomplete(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "player-zone-class-completion-incomplete.json"))
	if err != nil {
		t.Fatalf("read json: %s", err)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, bytes.NewReader(b))
	}

	ts := httptest.NewServer(http.HandlerFunc(handler))

	defer ts.Close()

	httpc := http.Client{}
	c := tempushttprpc.NewClient(httpc, ts.URL)

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)

	defer cancel()

	data := tempushttprpc.GetPlayerZoneClassCompletionData{
		MapName:   "jump_cow",
		ZoneType:  tempushttp.ZoneTypeMap,
		ZoneIndex: 1,
		PlayerID:  59983,
		Class:     tempushttp.ClassTypeSoldier,
	}

	response, err := c.GetPlayerZoneClassCompletion(ctx, data)
	if err != nil {
		t.Fatalf("request error: %s", err)
	}

	if response.Result.ID != 0 {
		t.Fatalf("response malformed")
	}
}
