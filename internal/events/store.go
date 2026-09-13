package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bughatti/sentinel/internal/db"
	"github.com/jackc/pgx/v5"
)

// Store wraps a *db.DB and provides high-level event/recording persistence.
type Store struct {
	db *db.DB
}

// NewStore creates a Store backed by the given DB.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// ─────────────────────────────────────────────────────────────────────────────
// Events
// ─────────────────────────────────────────────────────────────────────────────

// InsertEvent persists a new event row.
func (s *Store) InsertEvent(ctx context.Context, e *Event) error {
	boxJSON, err := json.Marshal(e.Box)
	if err != nil {
		return fmt.Errorf("events store: marshal box: %w", err)
	}
	regionJSON, err := json.Marshal(e.Region)
	if err != nil {
		return fmt.Errorf("events store: marshal region: %w", err)
	}
	dataJSON, err := json.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("events store: marshal data: %w", err)
	}

	const q = `
		INSERT INTO events (
			id, camera, label, sub_label, score, false_positive,
			start_time, end_time, has_clip, has_snapshot, retain_indefinitely,
			top_score, box, region, area,
			entered_zones, current_zones, data,
			model_hash, detector_type, model_type
		) VALUES (
			$1,$2,$3,$4,$5,$6,
			$7,$8,$9,$10,$11,
			$12,$13,$14,$15,
			$16,$17,$18,
			$19,$20,$21
		)`

	subLabel := ""
	if len(e.SubLabel) > 0 {
		subLabel = strings.Join(e.SubLabel, ",")
	}
	enteredZones := e.EnteredZones
	if enteredZones == nil {
		enteredZones = []string{}
	}
	currentZones := e.CurrentZones
	if currentZones == nil {
		currentZones = []string{}
	}

	_, err = s.db.Pool().Exec(ctx, q,
		e.ID, e.Camera, e.Label, subLabel, e.Score, e.FalsePositive,
		e.StartTime, e.EndTime, e.HasClip, e.HasSnapshot, e.RetainIndefinitely,
		e.TopScore, boxJSON, regionJSON, e.Area,
		enteredZones, currentZones, dataJSON,
		e.ModelHash, e.DetectorType, e.ModelType,
	)
	if err != nil {
		return fmt.Errorf("events store: insert event %s: %w", e.ID, err)
	}
	return nil
}

// UpdateEvent updates mutable fields on an existing event.
func (s *Store) UpdateEvent(ctx context.Context, e *Event) error {
	boxJSON, err := json.Marshal(e.Box)
	if err != nil {
		return fmt.Errorf("events store: marshal box: %w", err)
	}
	regionJSON, err := json.Marshal(e.Region)
	if err != nil {
		return fmt.Errorf("events store: marshal region: %w", err)
	}
	dataJSON, err := json.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("events store: marshal data: %w", err)
	}

	const q = `
		UPDATE events SET
			label=$2, score=$3, false_positive=$4,
			end_time=$5, has_clip=$6, has_snapshot=$7, retain_indefinitely=$8,
			top_score=$9, box=$10, region=$11, area=$12,
			entered_zones=$13, current_zones=$14, data=$15,
			updated_at=NOW()
		WHERE id=$1`

	enteredZones := e.EnteredZones
	if enteredZones == nil {
		enteredZones = []string{}
	}
	currentZones := e.CurrentZones
	if currentZones == nil {
		currentZones = []string{}
	}

	_, err = s.db.Pool().Exec(ctx, q,
		e.ID, e.Label, e.Score, e.FalsePositive,
		e.EndTime, e.HasClip, e.HasSnapshot, e.RetainIndefinitely,
		e.TopScore, boxJSON, regionJSON, e.Area,
		enteredZones, currentZones, dataJSON,
	)
	if err != nil {
		return fmt.Errorf("events store: update event %s: %w", e.ID, err)
	}
	return nil
}

// GetEvent fetches a single event by ID.
func (s *Store) GetEvent(ctx context.Context, id string) (*Event, error) {
	const q = `
		SELECT id, camera, label, sub_label, score, false_positive,
		       start_time, end_time, has_clip, has_snapshot, retain_indefinitely,
		       top_score, box, region, area,
		       entered_zones, current_zones, data,
		       model_hash, detector_type, model_type
		FROM events WHERE id=$1`

	row := s.db.Pool().QueryRow(ctx, q, id)
	e, err := scanEvent(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("events store: event %s not found", id)
		}
		return nil, fmt.Errorf("events store: get event %s: %w", id, err)
	}
	return e, nil
}

// ListEvents returns events matching the filter, ordered by start_time DESC.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]*Event, error) {
	var (
		wheres []string
		args   []any
		idx    = 1
	)

	if f.Camera != "" {
		wheres = append(wheres, fmt.Sprintf("camera=$%d", idx))
		args = append(args, f.Camera)
		idx++
	}
	if f.Label != "" {
		wheres = append(wheres, fmt.Sprintf("label=$%d", idx))
		args = append(args, f.Label)
		idx++
	}
	if f.SubLabel != "" {
		wheres = append(wheres, fmt.Sprintf("sub_label=$%d", idx))
		args = append(args, f.SubLabel)
		idx++
	}
	if f.After != nil {
		wheres = append(wheres, fmt.Sprintf("start_time>=$%d", idx))
		args = append(args, *f.After)
		idx++
	}
	if f.Before != nil {
		wheres = append(wheres, fmt.Sprintf("start_time<=$%d", idx))
		args = append(args, *f.Before)
		idx++
	}
	if f.HasClip != nil {
		wheres = append(wheres, fmt.Sprintf("has_clip=$%d", idx))
		args = append(args, *f.HasClip)
		idx++
	}
	if f.HasSnapshot != nil {
		wheres = append(wheres, fmt.Sprintf("has_snapshot=$%d", idx))
		args = append(args, *f.HasSnapshot)
		idx++
	}
	if f.FalsePositive != nil {
		wheres = append(wheres, fmt.Sprintf("false_positive=$%d", idx))
		args = append(args, *f.FalsePositive)
		idx++
	}
	if len(f.Zones) > 0 {
		wheres = append(wheres, fmt.Sprintf("entered_zones && $%d", idx))
		args = append(args, f.Zones)
		idx++
	}

	where := ""
	if len(wheres) > 0 {
		where = "WHERE " + strings.Join(wheres, " AND ")
	}

	limit := 100
	if f.Limit > 0 {
		limit = f.Limit
	}
	args = append(args, limit, f.Skip)

	q := fmt.Sprintf(`
		SELECT id, camera, label, sub_label, score, false_positive,
		       start_time, end_time, has_clip, has_snapshot, retain_indefinitely,
		       top_score, box, region, area,
		       entered_zones, current_zones, data,
		       model_hash, detector_type, model_type
		FROM events %s
		ORDER BY start_time DESC
		LIMIT $%d OFFSET $%d`,
		where, idx, idx+1,
	)

	rows, err := s.db.Pool().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("events store: list events: %w", err)
	}
	defer rows.Close()

	var out []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("events store: scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// scanEvent scans one row (from QueryRow or Query) into an Event.
func scanEvent(row pgx.Row) (*Event, error) {
	var (
		e        Event
		subLabel string
		boxJSON  []byte
		regJSON  []byte
		dataJSON []byte
	)
	err := row.Scan(
		&e.ID, &e.Camera, &e.Label, &subLabel, &e.Score, &e.FalsePositive,
		&e.StartTime, &e.EndTime, &e.HasClip, &e.HasSnapshot, &e.RetainIndefinitely,
		&e.TopScore, &boxJSON, &regJSON, &e.Area,
		&e.EnteredZones, &e.CurrentZones, &dataJSON,
		&e.ModelHash, &e.DetectorType, &e.ModelType,
	)
	if err != nil {
		return nil, err
	}
	if subLabel != "" {
		e.SubLabel = strings.Split(subLabel, ",")
	}
	if len(boxJSON) > 0 {
		_ = json.Unmarshal(boxJSON, &e.Box)
	}
	if len(regJSON) > 0 {
		_ = json.Unmarshal(regJSON, &e.Region)
	}
	if len(dataJSON) > 0 {
		_ = json.Unmarshal(dataJSON, &e.Data)
	}
	return &e, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Recordings
// ─────────────────────────────────────────────────────────────────────────────

// InsertRecording persists a new recording segment row.
func (s *Store) InsertRecording(ctx context.Context, r *Recording) error {
	const q = `
		INSERT INTO recordings (id, camera, path, start_time, end_time, duration, motion, objects, segment_size)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (path) DO NOTHING`
	_, err := s.db.Pool().Exec(ctx, q,
		r.ID, r.Camera, r.Path, r.StartTime, r.EndTime,
		r.Duration, r.Motion, r.Objects, r.SegmentSize,
	)
	if err != nil {
		return fmt.Errorf("events store: insert recording %s: %w", r.ID, err)
	}
	return nil
}

// ListRecordings returns recording segments matching the filter.
func (s *Store) ListRecordings(ctx context.Context, f RecordingFilter) ([]*Recording, error) {
	var (
		wheres []string
		args   []any
		idx    = 1
	)
	if f.Camera != "" {
		wheres = append(wheres, fmt.Sprintf("camera=$%d", idx))
		args = append(args, f.Camera)
		idx++
	}
	if f.After != nil {
		wheres = append(wheres, fmt.Sprintf("start_time>=$%d", idx))
		args = append(args, *f.After)
		idx++
	}
	if f.Before != nil {
		wheres = append(wheres, fmt.Sprintf("end_time<=$%d", idx))
		args = append(args, *f.Before)
		idx++
	}
	if f.Motion != nil {
		wheres = append(wheres, fmt.Sprintf("motion=$%d", idx))
		args = append(args, *f.Motion)
		idx++
	}

	where := ""
	if len(wheres) > 0 {
		where = "WHERE " + strings.Join(wheres, " AND ")
	}
	limit := 1000
	if f.Limit > 0 {
		limit = f.Limit
	}
	args = append(args, limit, f.Skip)

	q := fmt.Sprintf(`
		SELECT id, camera, path, start_time, end_time, duration, motion, objects, segment_size
		FROM recordings %s ORDER BY start_time ASC LIMIT $%d OFFSET $%d`,
		where, idx, idx+1,
	)

	rows, err := s.db.Pool().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("events store: list recordings: %w", err)
	}
	defer rows.Close()

	var out []*Recording
	for rows.Next() {
		r := &Recording{}
		if err := rows.Scan(&r.ID, &r.Camera, &r.Path, &r.StartTime, &r.EndTime,
			&r.Duration, &r.Motion, &r.Objects, &r.SegmentSize); err != nil {
			return nil, fmt.Errorf("events store: scan recording: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Face identities (pgvector) ──────────────────────────────────────────────

// FaceIdentity is an enrolled face.
type FaceIdentity struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	CreatedAt float64 `json:"created_at"`
}

// vectorLiteral formats a float slice as a pgvector text literal "[a,b,...]".
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v)*10 + 2)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// InsertFaceIdentity enrolls a face embedding under a name. sourceEventID may be
// empty. Returns the new identity id.
func (s *Store) InsertFaceIdentity(ctx context.Context, name string, embedding []float32, sourceEventID string) (int, error) {
	const q = `INSERT INTO face_identities (name, embedding, source_event_id)
	           VALUES ($1, $2::vector, $3) RETURNING id`
	var src any
	if sourceEventID != "" {
		src = sourceEventID
	}
	var id int
	if err := s.db.Pool().QueryRow(ctx, q, name, vectorLiteral(embedding), src).Scan(&id); err != nil {
		return 0, fmt.Errorf("events store: insert face identity: %w", err)
	}
	return id, nil
}

// MatchFace returns the closest enrolled identity to the embedding by cosine
// similarity (1 - cosine distance). ok is true only if sim >= threshold.
func (s *Store) MatchFace(ctx context.Context, embedding []float32, threshold float64) (name string, sim float64, ok bool, err error) {
	const q = `SELECT name, 1 - (embedding <=> $1::vector) AS sim
	           FROM face_identities
	           ORDER BY embedding <=> $1::vector
	           LIMIT 1`
	err = s.db.Pool().QueryRow(ctx, q, vectorLiteral(embedding)).Scan(&name, &sim)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("events store: match face: %w", err)
	}
	return name, sim, sim >= threshold, nil
}

// ListFaceIdentities returns all enrolled identities ordered by name.
func (s *Store) ListFaceIdentities(ctx context.Context) ([]FaceIdentity, error) {
	const q = `SELECT id, name, extract(epoch from created_at) FROM face_identities ORDER BY name, id`
	rows, err := s.db.Pool().Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("events store: list face identities: %w", err)
	}
	defer rows.Close()
	var out []FaceIdentity
	for rows.Next() {
		var fi FaceIdentity
		if err := rows.Scan(&fi.ID, &fi.Name, &fi.CreatedAt); err != nil {
			return nil, fmt.Errorf("events store: scan face identity: %w", err)
		}
		out = append(out, fi)
	}
	return out, rows.Err()
}

// DeleteFaceIdentity removes one enrolled embedding by id.
func (s *Store) DeleteFaceIdentity(ctx context.Context, id int) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM face_identities WHERE id=$1`, id)
	if err != nil {
		return 0, fmt.Errorf("events store: delete face identity: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteFaceIdentitiesByName removes all enrolled embeddings for a name.
func (s *Store) DeleteFaceIdentitiesByName(ctx context.Context, name string) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM face_identities WHERE name=$1`, name)
	if err != nil {
		return 0, fmt.Errorf("events store: delete face identities by name: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SetEventSubLabel sets the sub_label (e.g. a recognised face identity) on an event.
func (s *Store) SetEventSubLabel(ctx context.Context, id string, subLabels []string) error {
	sub := ""
	if len(subLabels) > 0 {
		sub = strings.Join(subLabels, ",")
	}
	const q = `UPDATE events SET sub_label = $2, updated_at = NOW() WHERE id = $1`
	if _, err := s.db.Pool().Exec(ctx, q, id, sub); err != nil {
		return fmt.Errorf("events store: set sub_label: %w", err)
	}
	return nil
}

// SetHasClip marks an event as having an extracted clip on disk.
func (s *Store) SetHasClip(ctx context.Context, id string) error {
	const q = `UPDATE events SET has_clip = true WHERE id = $1`
	if _, err := s.db.Pool().Exec(ctx, q, id); err != nil {
		return fmt.Errorf("events store: set has_clip: %w", err)
	}
	return nil
}

// ListExpiredEventMediaIDs returns the IDs of ended events for camera older than
// cutoff that HAVE a snapshot or clip on disk (so retention can delete those
// files), excluding retain_indefinitely events. Only media-bearing events are
// returned — the bulk row delete (DeleteEventsBefore) handles the rest, so we
// don't iterate the huge tail of media-less events just to stat missing files.
func (s *Store) ListExpiredEventMediaIDs(ctx context.Context, camera string, cutoff float64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5000
	}
	const q = `
		SELECT id FROM events
		WHERE camera = $1
		  AND end_time IS NOT NULL
		  AND end_time < $2
		  AND NOT retain_indefinitely
		  AND (has_snapshot OR has_clip)
		LIMIT $3`
	rows, err := s.db.Pool().Query(ctx, q, camera, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("events store: list expired media ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("events store: scan expired id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteEventsByIDs removes the given events by ID and returns rows deleted.
func (s *Store) DeleteEventsByIDs(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	const q = `DELETE FROM events WHERE id = ANY($1)`
	tag, err := s.db.Pool().Exec(ctx, q, ids)
	if err != nil {
		return 0, fmt.Errorf("events store: delete events by ids: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteEventsBefore removes ended events for camera older than cutoff,
// preserving anything flagged retain_indefinitely. Returns rows deleted.
// (In-progress events with a null end_time are never touched.)
func (s *Store) DeleteEventsBefore(ctx context.Context, camera string, cutoff float64) (int64, error) {
	const q = `
		DELETE FROM events
		WHERE camera = $1
		  AND end_time IS NOT NULL
		  AND end_time < $2
		  AND NOT retain_indefinitely`
	tag, err := s.db.Pool().Exec(ctx, q, camera, cutoff)
	if err != nil {
		return 0, fmt.Errorf("events store: delete events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteRecordingsBefore removes recordings for camera older than before and
// returns the number of rows deleted.
func (s *Store) DeleteRecordingsBefore(ctx context.Context, camera string, before float64) (int64, error) {
	const q = `DELETE FROM recordings WHERE camera=$1 AND end_time<$2`
	tag, err := s.db.Pool().Exec(ctx, q, camera, before)
	if err != nil {
		return 0, fmt.Errorf("events store: delete recordings: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetRecordingsSummary returns per-day summaries for a camera.
func (s *Store) GetRecordingsSummary(ctx context.Context, camera string) ([]RecordingSummary, error) {
	const q = `
		SELECT
			TO_CHAR(TO_TIMESTAMP(start_time) AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
			camera,
			SUM(duration)::REAL AS duration,
			SUM(CASE WHEN motion THEN duration ELSE 0 END)::REAL AS motion,
			0::REAL AS objects
		FROM recordings
		WHERE camera=$1
		GROUP BY day, camera
		ORDER BY day DESC`

	rows, err := s.db.Pool().Query(ctx, q, camera)
	if err != nil {
		return nil, fmt.Errorf("events store: recordings summary: %w", err)
	}
	defer rows.Close()

	var out []RecordingSummary
	for rows.Next() {
		var r RecordingSummary
		if err := rows.Scan(&r.Day, &r.Camera, &r.Duration, &r.Motion, &r.Objects); err != nil {
			return nil, fmt.Errorf("events store: scan summary: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentStationaryBox returns the bounding box of the most recent long-lived
// event for (camera, label) that ended within the last 5 minutes. A "long-
// lived" event (duration > 30s) indicates a stationary object (parked car,
// etc.). Returns nil if no such event exists.
func (s *Store) RecentStationaryBox(ctx context.Context, camera, label string) (*Box, error) {
	const q = `
		SELECT box FROM events
		WHERE camera = $1
		  AND label  = $2
		  AND end_time IS NOT NULL
		  AND end_time - start_time > 30
		  AND end_time > extract(epoch from now()) - 300
		ORDER BY end_time DESC
		LIMIT 1`

	row := s.db.Pool().QueryRow(ctx, q, camera, label)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("events store: recent stationary box: %w", err)
	}
	var b Box
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("events store: unmarshal box: %w", err)
	}
	return &b, nil
}
