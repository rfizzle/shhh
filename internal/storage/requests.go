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

type ProviderMetrics struct {
	Provider       string
	Model          string
	Count          int
	SuccessRate    float64
	AvgTTFT        *float64
	P95TTFT        *float64
	AvgDuration    *float64
	P95Duration    *float64
	TotalTokensIn  *int64
	TotalTokensOut *int64
	ExecCount      int
	ExecSuccessRate *float64
}

func (db *DB) MetricsSummary() ([]ProviderMetrics, error) {
	rows, err := db.sql.Query(`
		WITH ranked AS (
			SELECT provider, model, success, ttft_ms, duration_ms, tokens_in, tokens_out, exit_code,
			       PERCENT_RANK() OVER (PARTITION BY provider, model ORDER BY ttft_ms) AS ttft_rank,
			       PERCENT_RANK() OVER (PARTITION BY provider, model ORDER BY duration_ms) AS dur_rank
			FROM requests
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
			AVG(CASE WHEN exit_code IS NOT NULL THEN CAST(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END AS REAL) END) as exec_success_rate
		FROM ranked
		GROUP BY provider, model
		ORDER BY count DESC`)
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
		); err != nil {
			return nil, err
		}
		results = append(results, m)
	}
	return results, rows.Err()
}
