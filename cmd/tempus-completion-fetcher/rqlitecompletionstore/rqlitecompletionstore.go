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

func (db *DB) GetStaleZones(ctx context.Context, threshold time.Time) ([]completionstore.Zone, error) {
	const q = `
SELECT
	map_id,
	zone_type,
	zone_index,
	map_name
FROM
	zones
WHERE
	fetched < ?
LIMIT 5;
`

	param := gorqlite.ParameterizedStatement{
		Query:     q,
		Arguments: []any{threshold.UnixMilli()},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	zones := make([]completionstore.Zone, 0, 5)

	var (
		mapID     int
		zoneIndex int
		zoneType  string
		mapName   string
	)

	for results.Next() {

		if err := results.Scan(&mapID, &zoneType, &zoneIndex, &mapName); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}

		z := completionstore.Zone{
			MapID:     uint64(mapID),
			MapName:   mapName,
			ZoneType:  tempushttp.ZoneType(zoneType),
			ZoneIndex: uint8(zoneIndex),
		}

		zones = append(zones, z)
	}

	return zones, nil
}

func (db *DB) GetAllZoneClassInfo(ctx context.Context) ([]completionstore.ZoneClassInfo, error) {
	const q = `
SELECT
	map_id,
	zone_type,
	zone_index,
	class,
	map_name,
	custom_name,
	tier,
	completions
FROM
	zone_class_info
WHERE
	tier != 0;
`

	param := gorqlite.ParameterizedStatement{
		Query:     q,
		Arguments: []any{},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	zones := make([]completionstore.ZoneClassInfo, 0, 4100)

	var (
		mapID       int
		zoneType    string
		zoneIndex   int
		class       int
		mapName     string
		customName  string
		tier        int
		completions int
	)

	for results.Next() {

		if err := results.Scan(&mapID, &zoneType, &zoneIndex, &class, &mapName, &customName, &tier, &completions); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}

		info := completionstore.ZoneClassInfo{
			MapID:       uint64(mapID),
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			Class:       tempushttp.ClassType(class),
			MapName:     mapName,
			CustomName:  customName,
			Tier:        uint8(tier),
			Completions: uint32(completions),
		}

		zones = append(zones, info)
	}

	return zones, nil
}

func (db *DB) SetZonesFetched(ctx context.Context, zones []completionstore.Zone, t time.Time) error {
	params := make([]gorqlite.ParameterizedStatement, 0, len(zones))

	const q = "UPDATE zones SET fetched = ? WHERE map_id = ? AND zone_type = ? AND zone_index = ?"

	for _, z := range zones {
		p := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{t.UnixMilli(), z.MapID, z.ZoneType, z.ZoneIndex},
		}

		params = append(params, p)
	}

	dbresults, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range dbresults {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) InsertPlayerClassZoneResults(ctx context.Context, results []completionstore.PlayerClassZoneResult) error {
	params := make([]gorqlite.ParameterizedStatement, 0, len(results))

	const q1 = `
INSERT INTO
	player_class_zone_results (
		player_id,
		map_id,
		zone_type,
		zone_index,
		class,
		custom_name,
		map_name,
		tier,
		updated,
		rank,
		duration,
		date,
		completions
	)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT
	(player_id, map_id, zone_type, zone_index, class)
DO UPDATE SET
	custom_name = excluded.custom_name,
	map_name = excluded.map_name,
	tier = excluded.tier,
	updated = excluded.updated,
	rank = excluded.rank,
	duration = excluded.duration,
	date = excluded.date,
	completions = excluded.completions;
`

	latestUpdates := make(map[completionstore.PlayerMap]time.Time)

	for _, r := range results {
		p := gorqlite.ParameterizedStatement{
			Query: q1,
			Arguments: []any{
				r.PlayerID,
				r.MapID,
				r.ZoneType,
				r.ZoneIndex,
				r.Class,
				r.CustomName,
				r.MapName,
				r.Tier,
				r.Updated.UnixMilli(),
				r.Rank,
				r.Duration,
				r.Date.UnixMilli(),
				r.Completions,
			},
		}

		params = append(params, p)

		pm := completionstore.PlayerMap{
			PlayerID: r.PlayerID,
			MapID:    r.MapID,
		}

		latestUpdates[pm] = r.Updated
	}

	const q2 = `
INSERT INTO
	player_map_stats (
		player_id,
		map_id,
		latest_update,
		latest_processed_update,
		data
	)
VALUES
	(?, ?, ?, ?, ?)
ON CONFLICT
	(player_id, map_id)
DO UPDATE SET
	latest_update = excluded.latest_update
`

	for pm, t := range latestUpdates {
		p := gorqlite.ParameterizedStatement{
			Query:     q2,
			Arguments: []any{pm.PlayerID, pm.MapID, t.UnixMilli(), 0, "{}"},
		}

		params = append(params, p)
	}

	dbresults, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range dbresults {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) InsertZones(ctx context.Context, zones map[completionstore.Zone]struct{}) error {
	const q2 = `
INSERT INTO zones (map_id, zone_type, zone_index, map_name, updated, fetched)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (map_id, zone_type, zone_index) DO NOTHING;
`

	params := make([]gorqlite.ParameterizedStatement, 0, len(zones))

	const q1 = "DELETE FROM zones;"

	zonesDelete := gorqlite.ParameterizedStatement{
		Query:     q1,
		Arguments: []any{},
	}

	params = append(params, zonesDelete)

	updated := time.Now()

	for zone := range zones {
		param := gorqlite.ParameterizedStatement{
			Query:     q2,
			Arguments: []any{zone.MapID, zone.ZoneType, zone.ZoneIndex, zone.MapName, updated.UnixMilli(), 0},
		}

		params = append(params, param)
	}

	results, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) InsertMapStats(ctx context.Context, stats map[uint64]completionstore.MapStatsInfo) error {
	const q = `
INSERT INTO
	map_stats (
		map_id,
		map_name,
		data
	)
VALUES
	(?, ?, ?)
ON CONFLICT
	(map_id)
DO UPDATE SET
	map_name = excluded.map_name,
	data = excluded.data;
`

	params := make([]gorqlite.ParameterizedStatement, 0, len(stats))

	for mapID, info := range stats {
		b, err := json.Marshal(info.Stats)
		if err != nil {
			return fmt.Errorf("marshal stats: %w", err)
		}

		param := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{mapID, info.MapName, string(b)},
		}

		params = append(params, param)
	}

	results, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) SetPlayerMapsProcessed(ctx context.Context, maps []completionstore.StalePlayerMap) error {
	params := make([]gorqlite.ParameterizedStatement, 0, len(maps))

	const q = "UPDATE player_map_stats SET latest_processed_update = ? WHERE player_id = ? AND map_id = ?;"

	for _, m := range maps {
		p := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{m.LatestUpdate.UnixMilli(), m.PlayerID, m.MapID},
		}

		params = append(params, p)
	}

	dbresults, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range dbresults {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
}

func (db *DB) InsertPlayerMapStats(ctx context.Context, stats map[completionstore.PlayerMap]completionstore.PlayerMapStats) error {

	params := make([]gorqlite.ParameterizedStatement, 0, len(stats))

	const q1 = `
UPDATE
	player_map_stats
SET
	data = ?
WHERE
	player_id = ? AND map_id = ?;
`
	for pm, pmstats := range stats {
		b, err := json.Marshal(pmstats)
		if err != nil {
			return fmt.Errorf("marshal map stats: %w", err)
		}

		p := gorqlite.ParameterizedStatement{
			Query:     q1,
			Arguments: []any{string(b), pm.PlayerID, pm.MapID},
		}

		params = append(params, p)
	}

	results, _ := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w", r.Err)
		}
	}

	return nil
}

func (db *DB) GetPlayerMapZones(ctx context.Context, playerID, mapID uint64) (*completionstore.MapZones, bool, error) {

	const query = `
SELECT
	zone_class_info.zone_type,
	zone_class_info.zone_index,
	zone_class_info.class,
	zone_class_info.map_name,
	zone_class_info.custom_name,
	zone_class_info.tier,
	player_class_zone_results.rank,
	player_class_zone_results.duration,
	player_class_zone_results.date,
	zone_class_info.completions
FROM
	zone_class_info
LEFT JOIN
	player_class_zone_results
ON
	zone_class_info.map_id = player_class_zone_results.map_id AND
	zone_class_info.zone_type = player_class_zone_results.zone_type AND
	zone_class_info.zone_index = player_class_zone_results.zone_index AND
	zone_class_info.class = player_class_zone_results.class AND
	player_class_zone_results.player_id = ?
WHERE
	zone_class_info.map_id = ?;
`

	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID, mapID},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	n := results.NumRows()

	if n == 0 {
		return nil, false, nil
	}

	soldier := make([]completionstore.PlayerMapClassZoneCompletion, 0, n)
	demoman := make([]completionstore.PlayerMapClassZoneCompletion, 0, n)
	var mapName string

	for results.Next() {
		var (
			zoneType    string
			zoneIndex   int
			class       int
			customName  string
			tier        int
			rank        int
			duration    int
			date        int
			completions int
		)

		if err := results.Scan(&zoneType, &zoneIndex, &class, &mapName, &customName, &tier, &rank, &duration, &date, &completions); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		c := completionstore.PlayerMapClassZoneCompletion{
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			CustomName:  customName,
			Tier:        uint8(tier),
			Duration:    time.Duration(duration),
			Recorded:    time.UnixMilli(int64(date)),
			Rank:        uint32(rank),
			Completions: uint32(completions),
		}

		switch tempushttp.ClassType(class) {
		case tempushttp.ClassTypeDemoman:
			demoman = append(demoman, c)
		case tempushttp.ClassTypeSoldier:
			soldier = append(soldier, c)
		}
	}

	zones := &completionstore.MapZones{
		MapName: mapName,
		Soldier: soldier,
		Demoman: demoman,
	}

	return zones, true, nil
}

func (db *DB) InsertZoneClassInfo(ctx context.Context, info []completionstore.ZoneClassInfo) error {
	params := make([]gorqlite.ParameterizedStatement, 0, len(info))

	const q = `
INSERT INTO
	zone_class_info (
		map_id,
		zone_type,
		zone_index,
		class,
		map_name,
		custom_name,
		tier,
		completions
	)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT
	(map_id, zone_type, zone_index, class)
DO UPDATE SET
	map_name = excluded.map_name,
	custom_name = excluded.custom_name,
	tier = excluded.tier,
	completions = excluded.completions;
`
	for _, zi := range info {
		p := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{zi.MapID, zi.ZoneType, zi.ZoneIndex, zi.Class, zi.MapName, zi.CustomName, zi.Tier, zi.Completions},
		}

		params = append(params, p)
	}

	results, _ := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("result error: %w", r.Err)
		}
	}

	return nil
}

func (db *DB) GetCompletions(ctx context.Context, playerID uint64) (map[uint64]completionstore.PlayerMapStats, bool, error) {
	const query = `
SELECT
	map_stats.map_id,
	map_stats.map_name,
	player_map_stats.player_id,
	map_stats.data,
	player_map_stats.data
FROM
	map_stats
LEFT JOIN
	player_map_stats
ON
	map_stats.map_id = player_map_stats.map_id AND
	player_id = ?;
`
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

	// TODO: this is *so* slow
	playerMapStatsData := ""
	mapStatsData := ""

	var mapID int
	var mapName string
	var pid gorqlite.NullInt64

	stats := make(map[uint64]completionstore.PlayerMapStats, results.NumRows())

	for results.Next() {
		playerMapStatsData = playerMapStatsData[:0]
		mapStatsData = mapStatsData[:0]

		if err := results.Scan(&mapID, &mapName, &pid, &mapStatsData, &playerMapStatsData); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		if pid.Valid {
			var stat completionstore.PlayerMapStats

			if err := json.Unmarshal([]byte(playerMapStatsData), &stat); err != nil {
				return nil, false, fmt.Errorf("unmarshal player map stats data: %w", err)
			}

			stats[uint64(mapID)] = stat
		} else {
			var stat completionstore.MapStats

			if err := json.Unmarshal([]byte(mapStatsData), &stat); err != nil {
				return nil, false, fmt.Errorf("unmarshal map stats data: %w", err)
			}

			stats[uint64(mapID)] = completionstore.PlayerMapStats{
				MapID:   uint64(mapID),
				MapName: mapName,
				Soldier: completionstore.PlayerClassMapStats{
					TotalCompletionPercentage: 0,
					PointCompletionPercentage: 0,
					Tiers:                     stat.Soldier.Tiers,
					IncompleteTiers:           stat.Soldier.Tiers,
					TotalPointsAvailable:      stat.Soldier.PointsTotal,
					PointsAvailableByTier:     stat.Soldier.TierPointsTotal,
				},
				Demoman: completionstore.PlayerClassMapStats{
					TotalCompletionPercentage: 0,
					PointCompletionPercentage: 0,
					Tiers:                     stat.Demoman.Tiers,
					IncompleteTiers:           stat.Demoman.Tiers,
					TotalPointsAvailable:      stat.Demoman.PointsTotal,
					PointsAvailableByTier:     stat.Demoman.TierPointsTotal,
				},
			}
		}
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

func (db *DB) GetPlayerMapResults(ctx context.Context, playerMaps []completionstore.StalePlayerMap) (map[completionstore.PlayerMap][]completionstore.PlayerClassZoneResult, error) {
	const q = `
SELECT
	player_id,
	map_id,
	zone_type,
	zone_index,
	class,
	custom_name,
	map_name,
	tier,
	rank,
	duration,
	date,
	completions
FROM
	player_class_zone_results
WHERE
	player_id = ? AND map_id = ?;
`

	params := make([]gorqlite.ParameterizedStatement, 0, len(playerMaps))

	for _, pm := range playerMaps {
		param := gorqlite.ParameterizedStatement{
			Query:     q,
			Arguments: []any{pm.PlayerID, pm.MapID},
		}

		params = append(params, param)
	}

	dbresults, err := db.conn.QueryParameterizedContext(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("do query: %w", err)
	}

	var (
		playerID    int
		mapID       int
		zoneType    string
		zoneIndex   int
		class       int
		customName  string
		mapName     string
		tier        int
		rank        int
		duration    int
		date        int
		completions int
	)

	maps := make(map[completionstore.PlayerMap][]completionstore.PlayerClassZoneResult, len(playerMaps))

	for _, r := range dbresults {
		if r.Err != nil {
			return nil, fmt.Errorf("result error: %w", r.Err)
		}

		for r.Next() {
			if err := r.Scan(&playerID, &mapID, &zoneType, &zoneIndex, &class, &customName, &mapName, &tier, &rank, &duration, &date, &completions); err != nil {
				return nil, fmt.Errorf("scan results: %w", err)
			}

			pm := completionstore.PlayerMap{
				PlayerID: uint64(playerID),
				MapID:    uint64(mapID),
			}

			result := completionstore.PlayerClassZoneResult{
				MapID:       pm.MapID,
				ZoneType:    tempushttp.ZoneType(zoneType),
				ZoneIndex:   uint8(zoneIndex),
				PlayerID:    pm.PlayerID,
				Class:       tempushttp.ClassType(class),
				CustomName:  customName,
				MapName:     mapName,
				Tier:        uint8(tier),
				Rank:        uint32(rank),
				Duration:    time.Duration(duration),
				Date:        time.UnixMilli(int64(date)),
				Completions: uint32(completions),
			}

			results, ok := maps[pm]
			if !ok {
				const estsize = 6

				results = make([]completionstore.PlayerClassZoneResult, 0, estsize)
			}

			results = append(results, result)
			maps[pm] = results
		}
	}

	return maps, nil
}

func (db *DB) GetStalePlayerMaps(ctx context.Context) ([]completionstore.StalePlayerMap, error) {
	const query = `
SELECT
	player_id,
	map_id,
	latest_update
FROM
	player_map_stats
WHERE
	latest_update != latest_processed_update
LIMIT 10000;
`
	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{},
	}

	results, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("do query: %w: %w", err, results.Err)
	}

	var (
		playerID     int
		mapID        int
		latestUpdate int
	)

	playerMaps := make([]completionstore.StalePlayerMap, 0, results.NumRows())

	for results.Next() {
		if err := results.Scan(&playerID, &mapID, &latestUpdate); err != nil {
			return nil, fmt.Errorf("scan results: %w", err)
		}

		pm := completionstore.StalePlayerMap{
			PlayerMap: completionstore.PlayerMap{
				PlayerID: uint64(playerID),
				MapID:    uint64(mapID),
			},
			LatestUpdate: time.UnixMilli(int64(latestUpdate)),
		}

		playerMaps = append(playerMaps, pm)
	}

	return playerMaps, nil
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

CREATE TABLE zones (
	map_id     INTEGER NOT NULL,
	zone_type  TEXT    NOT NULL,
	zone_index INTEGER NOT NULL,
	map_name   TEXT    NOT NULL,
	updated    INTEGER NOT NULL,
	fetched    INTEGER NOT NULL,
	PRIMARY KEY (map_id, zone_type, zone_index)
);

CREATE TABLE map_stats (
	map_id        INTEGER NOT NULL,
	map_name      TEXT    NOT NULL,
	data          TEXT    NOT NULL,
	PRIMARY KEY (map_id)
);

CREATE TABLE zone_class_info (
	map_id        INTEGER NOT NULL,
	zone_type     TEXT    NOT NULL,
	zone_index    INTEGER NOT NULL,
	class         INTEGER NOT NULL,
	map_name      TEXT    NOT NULL,
	custom_name   TEXT    NOT NULL,
	tier          INTEGER NOT NULL,
	completions   INTEGER NOT NULL,
	PRIMARY KEY (map_id, zone_type, zone_index, class)
);

CREATE TABLE player_class_zone_results (
	player_id   INTEGER NOT NULL,
	map_id      INTEGER NOT NULL,
	zone_type   TEXT    NOT NULL,
	zone_index  INTEGER NOT NULL,
	class       INTEGER NOT NULL,
	custom_name TEXT    NOT NULL,
	map_name    TEXT    NOT NULL,
	tier        INTEGER NOT NULL,
	updated     INTEGER NOT NULL,
	rank        INTEGER NOT NULL,
	duration    INTEGER NOT NULL,
	date        INTEGER NOT NULL,
	completions INTEGER NOT NULL,
	PRIMARY KEY (player_id, map_id, zone_type, zone_index, class)
);

CREATE TABLE player_map_stats (
	player_id                   INTEGER NOT NULL,
	map_id                      INTEGER NOT NULL,
	latest_update               INTEGER NOT NULL,
	latest_processed_update     INTEGER NOT NULL,
	data                        TEXT    NOT NULL,
	PRIMARY KEY (player_id, map_id)
);

CREATE INDEX player_map_stats_times_index
ON player_map_stats (latest_update, latest_processed_update, player_id, map_id);
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
