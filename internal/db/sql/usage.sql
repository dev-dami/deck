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
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
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
