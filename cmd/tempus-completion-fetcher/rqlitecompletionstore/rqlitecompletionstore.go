package rqlitecompletionstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func (db *DB) GetPlayerResults(ctx context.Context, playerID uint64) ([]completionstore.PlayerClassZoneResult, bool, error) {

	const query = `
SELECT
	player_class_zone_results.map_id,
	player_class_zone_results.zone_type,
	player_class_zone_results.zone_index,
	player_class_zone_results.class,
	player_class_zone_results.map_name,
	player_class_zone_results.custom_name,
	player_class_zone_results.tier,
	player_class_zone_results.rank,
	player_class_zone_results.duration,
	player_class_zone_results.date,
	player_class_zone_results.completions
FROM
	player_class_zone_results
WHERE
	player_class_zone_results.player_id = ? AND
	player_class_zone_results.zone_type != 'trick'
ORDER BY
	player_class_zone_results.date DESC;
`

	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID},
	}

	dbresults, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, dbresults.Err)
	}

	n := dbresults.NumRows()

	if n == 0 {
		return nil, false, nil
	}

	results := make([]completionstore.PlayerClassZoneResult, 0, n)

	var (
		mapID       int
		zoneType    string
		zoneIndex   int
		class       int
		mapName     string
		customName  string
		tier        int
		rank        int
		duration    int
		date        int
		completions int
	)

	for dbresults.Next() {
		rank = 0
		duration = 0
		date = 0

		if err := dbresults.Scan(
			&mapID,
			&zoneType,
			&zoneIndex,
			&class,
			&mapName,
			&customName,
			&tier,
			&rank,
			&duration,
			&date,
			&completions,
		); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		result := completionstore.PlayerClassZoneResult{
			MapID:       uint64(mapID),
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			PlayerID:    uint64(playerID),
			Class:       tempushttp.ClassType(class),
			CustomName:  customName,
			MapName:     mapName,
			Tier:        uint8(tier),
			Rank:        uint32(rank),
			Duration:    time.Duration(duration),
			Date:        time.UnixMilli(int64(date)),
			Completions: uint32(completions),
		}

		results = append(results, result)
	}

	return results, true, nil
}

func (db *DB) GetPlayerMapClassResults(ctx context.Context, playerID, mapID uint64, class tempushttp.ClassType) ([]completionstore.PlayerClassZoneResult, bool, error) {

	const query = `
SELECT
	zone_class_info.zone_type,
	zone_class_info.zone_index,
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
	zone_class_info.map_id = ? AND
	zone_class_info.class = ? AND
	zone_class_info.zone_type != 'trick'
ORDER BY
	player_class_zone_results.date DESC;
`

	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID, mapID, class},
	}

	dbresults, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, dbresults.Err)
	}

	n := dbresults.NumRows()

	if n == 0 {
		return nil, false, nil
	}

	results := make([]completionstore.PlayerClassZoneResult, 0, n)

	var (
		zoneType    string
		zoneIndex   int
		mapName     string
		customName  string
		tier        int
		rank        int
		duration    int
		date        int
		completions int
	)

	for dbresults.Next() {
		rank = 0
		duration = 0
		date = 0

		if err := dbresults.Scan(
			&zoneType,
			&zoneIndex,
			&mapName,
			&customName,
			&tier,
			&rank,
			&duration,
			&date,
			&completions,
		); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		result := completionstore.PlayerClassZoneResult{
			MapID:       mapID,
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			PlayerID:    playerID,
			Class:       class,
			CustomName:  customName,
			MapName:     mapName,
			Tier:        uint8(tier),
			Rank:        uint32(rank),
			Duration:    time.Duration(duration),
			Date:        time.UnixMilli(int64(date)),
			Completions: uint32(completions),
		}

		results = append(results, result)
	}

	return results, true, nil
}

// TODO: consolidate with GetPlayerResults
func (db *DB) GetPlayerRecentResults(ctx context.Context, playerID uint64) ([]completionstore.PlayerClassZoneResult, bool, error) {
	const query = `
SELECT
	player_class_zone_results.map_id,
	player_class_zone_results.zone_type,
	player_class_zone_results.zone_index,
	player_class_zone_results.class,
	player_class_zone_results.map_name,
	player_class_zone_results.custom_name,
	player_class_zone_results.tier,
	player_class_zone_results.rank,
	player_class_zone_results.duration,
	player_class_zone_results.date,
	player_class_zone_results.completions
FROM
	player_class_zone_results
WHERE
	player_class_zone_results.player_id = ? AND
	player_class_zone_results.zone_type != 'trick'
ORDER BY
	date desc
LIMIT 10;
`

	param := gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{playerID},
	}

	dbresults, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, dbresults.Err)
	}

	n := dbresults.NumRows()

	if n == 0 {
		return nil, false, nil
	}

	results := make([]completionstore.PlayerClassZoneResult, 0, n)

	var (
		mapID       int
		zoneType    string
		zoneIndex   int
		class       int
		mapName     string
		customName  string
		tier        int
		rank        int
		duration    int
		date        int
		completions int
	)

	for dbresults.Next() {
		rank = 0
		duration = 0
		date = 0

		if err := dbresults.Scan(
			&mapID,
			&zoneType,
			&zoneIndex,
			&class,
			&mapName,
			&customName,
			&tier,
			&rank,
			&duration,
			&date,
			&completions,
		); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		result := completionstore.PlayerClassZoneResult{
			MapID:       uint64(mapID),
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			PlayerID:    uint64(playerID),
			Class:       tempushttp.ClassType(class),
			CustomName:  customName,
			MapName:     mapName,
			Tier:        uint8(tier),
			Rank:        uint32(rank),
			Duration:    time.Duration(duration),
			Date:        time.UnixMilli(int64(date)),
			Completions: uint32(completions),
		}

		results = append(results, result)
	}

	return results, true, nil
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

func (db *DB) GetPlayerClassZoneResults(ctx context.Context, playerID uint64, zoneTypes []string, tiers, classes []uint8) ([]completionstore.PlayerClassZoneResult, bool, error) {
	// TODO: probably don't need custom name, tier, duration, date, rank; just need to know if it was completed for the completions page
	const qStart = `
SELECT
	zone_class_info.map_id,
	zone_class_info.zone_type,
	zone_class_info.zone_index,
	zone_class_info.class,
	zone_class_info.custom_name,
	zone_class_info.map_name,
	zone_class_info.tier,
	zone_class_info.completions,
	player_class_zone_results.rank,
	player_class_zone_results.duration,
	player_class_zone_results.date
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
`

	args := make([]any, 0, 1+len(zoneTypes)+len(tiers)+len(classes))

	var whereClause strings.Builder

	args = append(args, playerID)

	whereClause.WriteString("zone_class_info.zone_type IN (")
	for i, zt := range zoneTypes {
		if i == 0 {
			whereClause.Write([]byte("?"))
		} else {
			whereClause.Write([]byte(",?"))
		}

		args = append(args, zt)
	}
	whereClause.WriteString(") AND ")

	whereClause.WriteString("zone_class_info.tier IN (")
	for i, t := range tiers {
		if i == 0 {
			whereClause.Write([]byte("?"))
		} else {
			whereClause.Write([]byte(",?"))
		}
		args = append(args, t)
	}
	whereClause.WriteString(") AND ")

	whereClause.WriteString("zone_class_info.class IN (")
	for i, c := range classes {
		if i == 0 {
			whereClause.Write([]byte("?"))
		} else {
			whereClause.Write([]byte(",?"))
		}
		args = append(args, c)
	}
	whereClause.WriteString(");")

	param := gorqlite.ParameterizedStatement{
		Query:     qStart + whereClause.String(),
		Arguments: args,
	}

	dbresults, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return nil, false, fmt.Errorf("do query: %w: %w", err, dbresults.Err)
	}

	if dbresults.NumRows() == 0 {
		return nil, false, nil
	}

	results := make([]completionstore.PlayerClassZoneResult, 0, dbresults.NumRows())

	var (
		mapID       int
		zoneType    string
		zoneIndex   int
		class       int
		customName  string
		mapName     string
		tier        int
		completions int
		rank        int
		duration    int
		date        int
	)

	for dbresults.Next() {
		rank = 0
		duration = 0
		date = 0

		if err := dbresults.Scan(
			&mapID,
			&zoneType,
			&zoneIndex,
			&class,
			&customName,
			&mapName,
			&tier,
			&completions,
			&rank,
			&duration,
			&date,
		); err != nil {
			return nil, false, fmt.Errorf("scan results: %w", err)
		}

		result := completionstore.PlayerClassZoneResult{
			MapID:       uint64(mapID),
			ZoneType:    tempushttp.ZoneType(zoneType),
			ZoneIndex:   uint8(zoneIndex),
			PlayerID:    uint64(playerID),
			Class:       tempushttp.ClassType(class),
			CustomName:  customName,
			MapName:     mapName,
			Tier:        uint8(tier),
			Rank:        uint32(rank),
			Duration:    time.Duration(duration),
			Date:        time.UnixMilli(int64(date)),
			Completions: uint32(completions),
		}

		results = append(results, result)
	}

	return results, true, nil
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

func (db *DB) InsertSteamIDs(ctx context.Context, steamIDs map[string]uint64) error {
	// steam IDs probably never change association?
	const query = `
INSERT INTO
	steam_ids (
		steam_id,
		player_id
	)
VALUES
	(?, ?)
ON CONFLICT
	(steam_id)
DO NOTHING;`

	params := make([]gorqlite.ParameterizedStatement, 0, len(steamIDs))

	for steamID, playerID := range steamIDs {
		param := gorqlite.ParameterizedStatement{
			Query:     query,
			Arguments: []any{steamID, playerID},
		}

		params = append(params, param)
	}

	dbresults, err := db.conn.WriteParameterizedContext(ctx, params)

	for _, r := range dbresults {
		if r.Err != nil {
			return fmt.Errorf("result error: %w: %w", err, r.Err)
		}
	}

	return nil
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

func (db *DB) GetPlayerBySteamID(ctx context.Context, steamID string) (uint64, bool, error) {
	const q = `
SELECT
	player_id
FROM
	steam_ids
WHERE
	steam_id = ?;
`
	param := gorqlite.ParameterizedStatement{
		Query:     q,
		Arguments: []any{steamID},
	}

	result, err := db.conn.QueryOneParameterizedContext(ctx, param)
	if err != nil {
		return 0, false, fmt.Errorf("do query: %w: %w", err, result.Err)
	}

	if result.NumRows() == 0 {
		return 0, false, nil
	}

	var playerID int

	for result.Next() {
		if err := result.Scan(&playerID); err != nil {
			return 0, false, fmt.Errorf("scan results: %w", err)
		}
	}

	return uint64(playerID), true, nil
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

CREATE TABLE steam_ids (
	steam_id     TEXT    NOT NULL,
	player_id    INTEGER NOT NULL,
	PRIMARY KEY (steam_id)
);

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
