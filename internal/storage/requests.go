package storage

import "time"

type RequestRecord struct {
	Provider  string
	Model     string
	Prompt    string
	Command   string
	Action    string
	TTFT      *time.Duration
	Duration  *time.Duration
	TokensIn  *int64
	TokensOut *int64
	Success   bool
}

func (db *DB) RecordRequest(r RequestRecord) (int64, error) {
	var ttftMs, durationMs *int64
	if r.TTFT != nil {
		v := r.TTFT.Milliseconds()
		ttftMs = &v
	}
	if r.Duration != nil {
		v := r.Duration.Milliseconds()
		durationMs = &v
	}
	success := 0
	if r.Success {
		success = 1
	}
	res, err := db.sql.Exec(
		`INSERT INTO requests (provider, model, prompt, command, action, ttft_ms, duration_ms, tokens_in, tokens_out, success)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Provider, r.Model, r.Prompt, r.Command, r.Action,
		ttftMs, durationMs, r.TokensIn, r.TokensOut, success,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) RecordExitCode(requestID int64, exitCode int) error {
	_, err := db.sql.Exec(`UPDATE requests SET exit_code = ? WHERE id = ?`, exitCode, requestID)
	return err
}

type UnratedRequest struct {
	ID        int64
	CreatedAt time.Time
	Prompt    string
	Command   string
	Action    string
	ExitCode  *int64
}

// ListUnrated returns recent requests that produced a command the user acted
// on but hasn't rated yet, newest first.
func (db *DB) ListUnrated(limit int) ([]UnratedRequest, error) {
	rows, err := db.sql.Query(
		`SELECT id, created_at, prompt, command, action, exit_code
		 FROM requests
		 WHERE rating IS NULL
		   AND command != ''
		   AND action IN ('run', 'run-all', 'run-step', 'copy', 'edit', 'save')
		 ORDER BY id DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UnratedRequest
	for rows.Next() {
		var (
			r         UnratedRequest
			createdAt string
		)
		if err := rows.Scan(&r.ID, &createdAt, &r.Prompt, &r.Command, &r.Action, &r.ExitCode); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse("2006-01-02T15:04:05.000Z", createdAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RateRequest records a thumbs-up (true) or thumbs-down (false) for a request.
func (db *DB) RateRequest(id int64, up bool) error {
	rating := 0
	if up {
		rating = 1
	}
	_, err := db.sql.Exec(`UPDATE requests SET rating = ? WHERE id = ?`, rating, id)
	return err
}

type ProviderMetrics struct {
	Provider        string
	Model           string
	Count           int
	SuccessRate     float64
	AvgTTFT         *float64
	P95TTFT         *float64
	AvgDuration     *float64
	P95Duration     *float64
	TotalTokensIn   *int64
	TotalTokensOut  *int64
	ExecCount       int
	ExecSuccessRate *float64
	RatedCount      int
	RatingRate      *float64
}

// MetricsSummary aggregates recorded requests per provider and model since
// the cutoff, most-used first. A zero cutoff is every request ever recorded,
// which is what `shhh metrics` reads without a --window.
func (db *DB) MetricsSummary(since time.Time) ([]ProviderMetrics, error) {
	rows, err := db.sql.Query(`
		WITH ranked AS (
			SELECT provider, model, success, ttft_ms, duration_ms, tokens_in, tokens_out, exit_code, rating,
			       PERCENT_RANK() OVER (PARTITION BY provider, model ORDER BY ttft_ms) AS ttft_rank,
			       PERCENT_RANK() OVER (PARTITION BY provider, model ORDER BY duration_ms) AS dur_rank
			FROM requests WHERE created_at >= ?
		)
		SELECT
			provider, model,
			COUNT(*) as count,
			AVG(CAST(success AS REAL)) as success_rate,
			AVG(ttft_ms) as avg_ttft,
			MAX(CASE WHEN ttft_rank >= 0.95 THEN ttft_ms END) as p95_ttft,
			AVG(duration_ms) as avg_duration,
			MAX(CASE WHEN dur_rank >= 0.95 THEN duration_ms END) as p95_duration,
			SUM(tokens_in) as total_tokens_in,
			SUM(tokens_out) as total_tokens_out,
			COUNT(exit_code) as exec_count,
			AVG(CASE WHEN exit_code IS NOT NULL THEN CAST(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END AS REAL) END) as exec_success_rate,
			COUNT(rating) as rated_count,
			AVG(CAST(rating AS REAL)) as rating_rate
		FROM ranked
		GROUP BY provider, model
		ORDER BY count DESC`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ProviderMetrics
	for rows.Next() {
		var m ProviderMetrics
		if err := rows.Scan(
			&m.Provider, &m.Model, &m.Count, &m.SuccessRate,
			&m.AvgTTFT, &m.P95TTFT, &m.AvgDuration, &m.P95Duration,
			&m.TotalTokensIn, &m.TotalTokensOut,
			&m.ExecCount, &m.ExecSuccessRate,
			&m.RatedCount, &m.RatingRate,
		); err != nil {
			return nil, err
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

// MetricsDayTokens is one model's token use on one calendar day (UTC, the way
// every row is stamped). It is what the per-model sparkline of §19c is drawn
// from: the columns were always in `requests` and nothing had ever read them
// by day.
type MetricsDayTokens struct {
	Provider  string
	Model     string
	Day       string
	TokensIn  int64
	TokensOut int64
}

// MetricsTokensByDay aggregates request tokens per provider/model per day
// since the cutoff, oldest day first.
func (db *DB) MetricsTokensByDay(since time.Time) ([]MetricsDayTokens, error) {
	rows, err := db.sql.Query(
		`SELECT provider, model, substr(created_at, 1, 10) AS day,
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0)
		 FROM requests WHERE created_at >= ?
		 GROUP BY provider, model, day ORDER BY day`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MetricsDayTokens
	for rows.Next() {
		var d MetricsDayTokens
		if err := rows.Scan(&d.Provider, &d.Model, &d.Day, &d.TokensIn, &d.TokensOut); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MetricsActionUsage is what became of the commands one model answered with,
// and what those answers cost in tokens. Success is carried alongside the
// action because a request that never answered is not a category of what was
// done with it — it is the cost of nothing having been done at all.
type MetricsActionUsage struct {
	Provider  string
	Model     string
	Action    string
	Success   bool
	Count     int
	TokensIn  int64
	TokensOut int64
}

// MetricsByAction aggregates requests by provider/model/action/success since
// the cutoff, most-frequent first. The model stays in the grouping because
// tokens are priced per model: a split of spend that summed tokens across
// models first would be pricing gpt-5.2's output at gemini's rate.
func (db *DB) MetricsByAction(since time.Time) ([]MetricsActionUsage, error) {
	rows, err := db.sql.Query(
		`SELECT provider, model, action, success, COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0)
		 FROM requests WHERE created_at >= ?
		 GROUP BY provider, model, action, success ORDER BY COUNT(*) DESC`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MetricsActionUsage
	for rows.Next() {
		var (
			u       MetricsActionUsage
			success int
		)
		if err := rows.Scan(&u.Provider, &u.Model, &u.Action, &success,
			&u.Count, &u.TokensIn, &u.TokensOut); err != nil {
			return nil, err
		}
		u.Success = success != 0
		out = append(out, u)
	}
	return out, rows.Err()
}
