CREATE TABLE IF NOT EXISTS {{schema}}summary_cache (
    content_hash     TEXT NOT NULL,
    prompt_version   TEXT NOT NULL,
    self_hash        TEXT NOT NULL,
    dep_hash         TEXT NOT NULL,
    kind             TEXT NOT NULL,
    model_id         TEXT NOT NULL DEFAULT '',
    summary_json     TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL,
    PRIMARY KEY (content_hash, prompt_version)
);
CREATE INDEX IF NOT EXISTS {{schema}}idx_summary_cache_self ON summary_cache(self_hash, prompt_version, created_at DESC);
CREATE INDEX IF NOT EXISTS {{schema}}idx_summary_cache_lru ON summary_cache(last_accessed_at);

CREATE TABLE IF NOT EXISTS {{schema}}symbol_history (
    symbol_id         TEXT NOT NULL,
    prompt_version    TEXT NOT NULL,
    last_content_hash TEXT NOT NULL,
    last_self_hash    TEXT NOT NULL,
    last_dep_hash     TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (symbol_id, prompt_version)
);
