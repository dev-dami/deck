-- name: InsertUsage :exec
INSERT INTO usage (
    id,
    session_id,
    agent_id,
    kind,
    model,
    input_tokens,
    output_tokens,
    cached_tokens,
    cost_usd,
    duration_ms,
    created_at,
    cache_write_tokens,
    reasoning_tokens,
    status,
    ttft_ms,
    api_ms,
    "trigger",
    mode
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: UsageTotals :one
SELECT
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cached_tokens), 0) AS INTEGER) AS cached_tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd,
    CAST(COALESCE(SUM(duration_ms), 0) AS INTEGER) AS duration_ms
FROM usage
WHERE created_at >= sqlc.arg(since);

-- name: UsageByDay :many
-- Days are local calendar days, as the user counts them.
SELECT
    CAST(date(created_at, 'unixepoch', 'localtime') AS TEXT) AS day,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens + output_tokens), 0) AS INTEGER) AS tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY day
ORDER BY day ASC;

-- name: UsageByKind :many
SELECT
    kind,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cached_tokens), 0) AS INTEGER) AS cached_tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY kind
ORDER BY cost_usd DESC, turns DESC, kind ASC;

-- name: UsageByModel :many
SELECT
    kind,
    model,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cached_tokens), 0) AS INTEGER) AS cached_tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY kind, model
ORDER BY cost_usd DESC, turns DESC, kind ASC, model ASC;

-- name: UsageTopSessions :many
-- Sessions keep their usage rows after they are deleted, so the title can be
-- missing.
SELECT
    u.session_id,
    CAST(COALESCE(s.title, '') AS TEXT) AS title,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(u.input_tokens + u.output_tokens), 0) AS INTEGER) AS tokens,
    CAST(COALESCE(SUM(u.cost_usd), 0) AS REAL) AS cost_usd
FROM usage u
LEFT JOIN sessions s ON s.id = u.session_id
WHERE u.created_at >= sqlc.arg(since)
GROUP BY u.session_id
ORDER BY cost_usd DESC, tokens DESC, u.session_id ASC
LIMIT sqlc.arg(limit);

-- name: UsageKindStats :many
-- ttft_sum and ttft_turns cover only the turns whose TTFT is known.
SELECT
    kind,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cached_tokens), 0) AS INTEGER) AS cached_tokens,
    CAST(COALESCE(SUM(cache_write_tokens), 0) AS INTEGER) AS cache_write_tokens,
    CAST(COALESCE(SUM(reasoning_tokens), 0) AS INTEGER) AS reasoning_tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd,
    CAST(COALESCE(SUM(CASE WHEN cost_usd > 0 THEN 1 ELSE 0 END), 0) AS INTEGER) AS cost_turns,
    CAST(COALESCE(SUM(duration_ms), 0) AS INTEGER) AS duration_ms,
    CAST(COALESCE(SUM(ttft_ms), 0) AS INTEGER) AS ttft_sum,
    CAST(COALESCE(SUM(CASE WHEN ttft_ms > 0 THEN 1 ELSE 0 END), 0) AS INTEGER) AS ttft_turns
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY kind
ORDER BY turns DESC, kind ASC;

-- name: UsageModelStats :many
SELECT
    kind,
    model,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cached_tokens), 0) AS INTEGER) AS cached_tokens,
    CAST(COALESCE(SUM(cache_write_tokens), 0) AS INTEGER) AS cache_write_tokens,
    CAST(COALESCE(SUM(reasoning_tokens), 0) AS INTEGER) AS reasoning_tokens,
    CAST(COALESCE(SUM(ttft_ms), 0) AS INTEGER) AS ttft_sum,
    CAST(COALESCE(SUM(CASE WHEN ttft_ms > 0 THEN 1 ELSE 0 END), 0) AS INTEGER) AS ttft_turns
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY kind, model
ORDER BY turns DESC, kind ASC, model ASC;

-- name: UsageByStatus :many
SELECT
    status,
    COUNT(*) AS turns
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY status
ORDER BY status ASC;

-- name: UsageByTrigger :many
SELECT
    "trigger",
    COUNT(*) AS turns
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY "trigger"
ORDER BY "trigger" ASC;

-- name: UsageTTFTs :many
-- Every known time to first token in the range, for percentiles.
SELECT ttft_ms
FROM usage
WHERE created_at >= sqlc.arg(since) AND ttft_ms > 0
ORDER BY ttft_ms ASC;

-- name: UsageSeries :many
-- bucket is the local time formatted with format ('%Y-%m-%d %H' for hours,
-- '%Y-%m-%d' for days).
SELECT
    CAST(strftime(sqlc.arg(format), created_at, 'unixepoch', 'localtime') AS TEXT) AS bucket,
    kind,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens + output_tokens), 0) AS INTEGER) AS tokens
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY bucket, kind
ORDER BY bucket ASC, kind ASC;

-- name: UsageRecent :many
SELECT
    id,
    session_id,
    agent_id,
    kind,
    model,
    input_tokens,
    output_tokens,
    cached_tokens,
    cache_write_tokens,
    reasoning_tokens,
    cost_usd,
    duration_ms,
    ttft_ms,
    api_ms,
    status,
    "trigger",
    mode,
    created_at
FROM usage
ORDER BY created_at DESC, rowid DESC
LIMIT sqlc.arg(limit);

-- name: InsertBehavior :exec
INSERT INTO behavior (
    session_id,
    agent_id,
    kind,
    detail,
    value,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?
);

-- name: BehaviorCounts :many
SELECT
    kind,
    detail,
    COUNT(*) AS count,
    CAST(COALESCE(SUM(value), 0) AS INTEGER) AS value
FROM behavior
WHERE created_at >= sqlc.arg(since)
GROUP BY kind, detail
ORDER BY kind ASC, detail ASC;

-- name: BehaviorByDay :many
-- Days are local calendar days, as the user counts them.
SELECT
    CAST(date(created_at, 'unixepoch', 'localtime') AS TEXT) AS day,
    kind,
    COUNT(*) AS count
FROM behavior
WHERE created_at >= sqlc.arg(since)
GROUP BY day, kind
ORDER BY day ASC, kind ASC;

-- name: InsertTraceSpan :exec
INSERT INTO trace_span (
    turn_id,
    session_id,
    agent_id,
    kind,
    name,
    start_ms,
    dur_ms,
    tokens,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: TraceSpans :many
SELECT
    kind,
    name,
    start_ms,
    dur_ms,
    tokens
FROM trace_span
WHERE turn_id = sqlc.arg(turn_id)
ORDER BY start_ms ASC, id ASC;

-- name: InsertLimitSnapshot :exec
INSERT INTO limit_snapshot (
    agent_kind,
    window_id,
    kind,
    label,
    used_pct,
    resets_at,
    duration_mins,
    plan,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: LatestLimitSnapshot :one
SELECT
    agent_kind,
    window_id,
    kind,
    label,
    used_pct,
    resets_at,
    duration_mins,
    plan,
    created_at
FROM limit_snapshot
WHERE agent_kind = sqlc.arg(agent_kind) AND window_id = sqlc.arg(window_id)
ORDER BY id DESC
LIMIT 1;

-- name: LatestLimitSnapshots :many
-- The newest row of every window of every agent kind.
SELECT
    agent_kind,
    window_id,
    kind,
    label,
    used_pct,
    resets_at,
    duration_mins,
    plan,
    created_at
FROM limit_snapshot
WHERE id IN (SELECT MAX(id) FROM limit_snapshot GROUP BY agent_kind, window_id)
ORDER BY agent_kind ASC, window_id ASC;

-- name: LimitHistory :many
SELECT
    agent_kind,
    window_id,
    kind,
    label,
    used_pct,
    resets_at,
    duration_mins,
    plan,
    created_at
FROM limit_snapshot
WHERE agent_kind = sqlc.arg(agent_kind) AND window_id = sqlc.arg(window_id) AND created_at >= sqlc.arg(since)
ORDER BY created_at ASC, id ASC;

-- name: LimitTimeline :many
-- Every window's rows since since, plus each window's last row before it
-- (the value the window held when the range opens), oldest first.
SELECT
    agent_kind,
    window_id,
    kind,
    label,
    used_pct,
    resets_at,
    duration_mins,
    plan,
    created_at
FROM limit_snapshot
WHERE created_at >= sqlc.arg(since)
   OR id IN (SELECT MAX(id) FROM limit_snapshot WHERE created_at < sqlc.arg(since) GROUP BY agent_kind, window_id)
ORDER BY agent_kind ASC, window_id ASC, created_at ASC, id ASC;

-- name: PruneLimitSnapshots :execrows
-- Drops rows older than before, but never a window's newest row.
DELETE FROM limit_snapshot
WHERE created_at < sqlc.arg(before)
  AND id NOT IN (SELECT MAX(id) FROM limit_snapshot GROUP BY agent_kind, window_id);

-- name: UsageHourly :many
-- Turns, tokens and cost per hour (Unix seconds at the hour's start), agent
-- kind and agent, since since.
SELECT
    CAST((created_at / 3600) * 3600 AS INTEGER) AS hour,
    kind,
    agent_id,
    COUNT(*) AS turns,
    CAST(COALESCE(SUM(input_tokens + output_tokens), 0) AS INTEGER) AS tokens,
    CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS cost_usd
FROM usage
WHERE created_at >= sqlc.arg(since)
GROUP BY hour, kind, agent_id
ORDER BY hour ASC, kind ASC, agent_id ASC;
