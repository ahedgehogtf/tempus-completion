package rqlitecompletionstore

import (
	"context"
	"encoding/json"
	"fmt"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/tempushttp"
	"time"

	"github.com/rqlite/gorqlite"
)

type DB struct {
	conn *gorqlite.Connection
}

func New(addr string) (*DB, error) {
	conn, err := gorqlite.Open(addr)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}

	// if err := conn.SetConsistencyLevel(gorqlite.ConsistencyLevelStrong); err != nil {
	// 	return nil, fmt.Errorf("set consistency level: %w", err)
	// }

	if err := conn.SetExecutionWithTransaction(true); err != nil {
		return nil, fmt.Errorf("set execution with transaction: %w", err)
	}

	db := &DB{
		conn: conn,
	}

	return db, nil
}

func (db *DB) GetPlayerFetchTime(ctx context.Context, playerID uint64) (time.Time, bool, error) {
	var t time.Time

	const query = `
SELECT
	fetched
FROM
	player_map_aggregations
INNER JOIN
	raw_player_completions
ON
	raw_player_completions.player_id = player_map_aggregations.player_id
WHERE
	raw_player_completions.player_id = ?;`

	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return t, false, fmt.Errorf("do query: %w", err)
	}

	if results.NumRows() == 0 {
		return t, false, nil
	}

	var (
		fetched int64
	)

	for results.Next() {
		if err := results.Scan(&fetched); err != nil {
			return t, false, fmt.Errorf("scan results: %w", err)
		}
	}

	return time.UnixMilli(fetched), true, nil
}

func (db *DB) GetStalePlayers(ctx context.Context) ([]completionstore.Player, error) {
	threshold := time.Now().Add(-24 * time.Hour).UnixMilli()

	const query = "SELECT player_id FROM raw_player_completions WHERE updated < ? LIMIT 10;"
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{threshold},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w", err)
	}

	var (
		id int64
	)

	players := make([]completionstore.Player, 0, results.NumRows())

	for results.Next() {
		if err := results.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}
		p := completionstore.Player{
			PlayerID: uint64(id),
		}

		players = append(players, p)
	}

	return players, nil
}

func (db *DB) InsertPlayerResults(ctx context.Context, playerResults map[uint64][]completionstore.CompletionResult) error {
	const q = `
INSERT INTO raw_player_completions (player_id, updated, data)
VALUES (?, ?, ?)
ON CONFLICT (player_id) DO UPDATE SET
updated = excluded.updated,
data = excluded.data;
`

	params := make([]gorqlite.ParameterizedStatement, 0, len(playerResults))

	updated := time.Now().UnixMilli()

	for p, completions := range playerResults {
		b, err := json.Marshal(completions)
		if err != nil {
			return fmt.Errorf("marshal completions: %w", err)
		}

		param := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{p, updated, string(b)},
		}

		params = append(params, param)
	}

	results, err := db.conn.WriteParameterizedContext(ctx, params)
	if err != nil {
		return fmt.Errorf("do query: %w", err)
	}

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w", err)
		}
	}

	return nil
}

func (db *DB) InsertTransformedPlayerData(ctx context.Context, playerID uint64, data completionstore.TransformedPlayerData) error {

	params := make([]gorqlite.ParameterizedStatement, 0, 2+len(data.MapZones))

	b, err := json.Marshal(data.Maps.Stats)
	if err != nil {
		return fmt.Errorf("marshal map stats: %w", err)
	}

	const q1 = `
INSERT INTO player_map_aggregations (player_id, fetched, data)
VALUES (?, ?, ?)
ON CONFLICT (player_id) DO UPDATE SET
fetched = excluded.fetched,
data = excluded.data;
`

	mapsInsert := gorqlite.ParameterizedStatement{
		Query:     q1,
		Arguments: []any{playerID, data.Maps.Fetched.UnixMilli(), string(b)},
	}

	params = append(params, mapsInsert)

	const q2 = "DELETE FROM player_map_zones WHERE player_id = ?;"

	zonesDelete := gorqlite.ParameterizedStatement{
		Query:     q2,
		Arguments: []any{playerID},
	}

	params = append(params, zonesDelete)

	for mapID, zones := range data.MapZones {
		b, err := json.Marshal(zones)
		if err != nil {
			return fmt.Errorf("marshal zones: %w", err)
		}

		const q3 = `
INSERT INTO player_map_zones (player_id, map_id, data)
VALUES (?, ?, ?);
`

		param := gorqlite.ParameterizedStatement{
			Query:     q3,
			Arguments: []any{playerID, mapID, string(b)},
		}

		params = append(params, param)
	}

	results, err := db.conn.WriteParameterizedContext(ctx, params)
	if err != nil {
		return fmt.Errorf("do query: %w", err)
	}

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) GetPlayerMapZones(ctx context.Context, playerID, mapID uint64) (*completionstore.MapZones, bool, error) {

	const query = "SELECT data FROM player_map_zones WHERE player_id = ? AND map_id = ?;"
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID, mapID},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	if results.NumRows() == 0 {
		return nil, false, nil
	}

	data := make([]byte, 0, 1000000)

	for results.Next() {
		if err := results.Scan(&data); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}
	}

	var zones completionstore.MapZones

	if err := json.Unmarshal(data, &zones); err != nil {
		return nil, false, fmt.Errorf("unmarshal data: %w", err)
	}

	return &zones, true, nil
}

func (db *DB) GetCompletions(ctx context.Context, playerID uint64) ([]completionstore.PlayerMapStats, bool, error) {
	const query = "SELECT data FROM player_map_aggregations WHERE player_id = ?;"
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	if results.NumRows() == 0 {
		return nil, false, nil
	}

	data := make([]byte, 0, 1000000)

	for results.Next() {
		if err := results.Scan(&data); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}
	}

	var stats []completionstore.PlayerMapStats

	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, false, fmt.Errorf("unmarshal data: %w", err)
	}

	return stats, true, nil
}

func (db *DB) InsertMaps(ctx context.Context, list *completionstore.MapList) error {
	const q = `
INSERT INTO kv (key, value, updated)
VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
updated = excluded.updated,
value = excluded.value;
`

	updated := list.Updated.UnixMilli()

	b, err := json.Marshal(list.Response)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	param := gorqlite.ParameterizedStatement{
		Query:     q,
		Arguments: []any{mapskey, string(b), updated},
	}

	results, err := db.conn.WriteOneParameterizedContext(ctx, param)
	if err != nil {
		return fmt.Errorf("do query: %w: %s", err, results.Err)
	}

	return nil
}

const mapskey = "map"

func (db *DB) GetMaps(ctx context.Context) (*completionstore.MapList, error) {
	const query = "SELECT value, updated FROM kv WHERE key = ?;"
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{mapskey},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w", err)
	}

	if results.NumRows() == 0 {
		return &completionstore.MapList{}, nil
	}

	data := make([]byte, 0, 1000000)
	var updated int64

	for results.Next() {
		if err := results.Scan(&data, &updated); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}
	}

	var response tempushttp.GetDetailedMapListResponse

	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("unmarshal data: %w", err)
	}

	list := &completionstore.MapList{
		Updated:  time.UnixMilli(updated),
		Response: response,
	}

	return list, nil
}

func (db *DB) GetStaleRawCompletions(ctx context.Context) (map[uint64]completionstore.RawPlayerCompletions, error) {
	const query = `
SELECT
	raw_player_completions.player_id,
	raw_player_completions.updated,
	raw_player_completions.data
FROM
	raw_player_completions
LEFT JOIN
	player_map_aggregations
ON
	player_map_aggregations.player_id = raw_player_completions.player_id
WHERE
	player_map_aggregations.fetched != raw_player_completions.updated
LIMIT 5;
`
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	data := make([]byte, 0, 1000000)
	var playerid int64
	var updated int64

	players := make(map[uint64]completionstore.RawPlayerCompletions, results.NumRows())

	for results.Next() {
		if err := results.Scan(&playerid, &updated, &data); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}

		var playerResults []completionstore.CompletionResult

		if err := json.Unmarshal(data, &playerResults); err != nil {
			return nil, fmt.Errorf("unmarshal data: %w", err)
		}

		completions := completionstore.RawPlayerCompletions{
			Updated: time.UnixMilli(updated),
			Results: playerResults,
		}

		players[uint64(playerid)] = completions
		data = data[:0]
	}

	return players, nil
}

func (db *DB) CreateSchema(ctx context.Context) error {
	const query = `
CREATE TABLE kv (
	key     TEXT     NOT NULL,
	updated INTEGER  NOT NULL,
	value   TEXT     NOT NULL,
	PRIMARY KEY (key)
);

CREATE INDEX kv_updated_index
ON kv (updated);

CREATE TABLE raw_player_completions (
	player_id      INTEGER  NOT NULL,
	updated        INTEGER  NOT NULL,
	data           TEXT     NOT NULL,
	PRIMARY KEY (player_id)
);

CREATE INDEX raw_player_completions_updated_index
ON raw_player_completions (updated);

CREATE TABLE player_map_aggregations (
	player_id     INTEGER NOT NULL,
	fetched       INTEGER NOT NULL,
	data          TEXT    NOT NULL,
	PRIMARY KEY (player_id)
);

CREATE INDEX player_map_aggregations_fetched_index
ON player_map_aggregations (fetched);

CREATE TABLE player_map_zones (
	player_id     INTEGER NOT NULL,
	map_id        INTEGER NOT NULL,
	data          TEXT    NOT NULL,
	PRIMARY KEY (player_id, map_id)
);
`
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{},
	}

	result, err := db.conn.WriteOneParameterizedContext(ctx, param)
	if err != nil {
		return fmt.Errorf("do query: %w", err)
	}

	if result.Err != nil {
		return fmt.Errorf("result error: %w", err)
	}

	return nil
}
