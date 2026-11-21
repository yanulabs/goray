CREATE TABLE IF NOT EXISTS {{schema}}working_symbols (
    id              TEXT PRIMARY KEY,
    parent_id       TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK (kind IN ('module','package','file','type','func','method')),
    module_name     TEXT NOT NULL DEFAULT '',
    pkg_path        TEXT NOT NULL DEFAULT '',
    file_path       TEXT NOT NULL DEFAULT '',
    start_line      INTEGER NOT NULL DEFAULT 0,
    end_line        INTEGER NOT NULL DEFAULT 0,
    loc             INTEGER NOT NULL DEFAULT 0,
    complexity      INTEGER NOT NULL DEFAULT 0,
    fan_in          INTEGER NOT NULL DEFAULT 0,
    is_exported     INTEGER NOT NULL DEFAULT 0,
    is_dead         INTEGER NOT NULL DEFAULT 0,
    is_eligible     INTEGER NOT NULL DEFAULT 0,
    sig_hash        TEXT NOT NULL DEFAULT '',
    self_hash       TEXT NOT NULL DEFAULT '',
    dep_hash        TEXT NOT NULL DEFAULT '',
    content_hash    TEXT NOT NULL DEFAULT '',
    updated_gen     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS {{schema}}idx_working_symbols_parent ON working_symbols(parent_id);
CREATE INDEX IF NOT EXISTS {{schema}}idx_working_symbols_kind_module ON working_symbols(kind, module_name);
CREATE INDEX IF NOT EXISTS {{schema}}idx_working_symbols_content ON working_symbols(content_hash);
CREATE INDEX IF NOT EXISTS {{schema}}idx_working_symbols_gen ON working_symbols(updated_gen);

CREATE TABLE IF NOT EXISTS {{schema}}working_edges (
    src_id  TEXT NOT NULL,
    dst_id  TEXT NOT NULL,
    kind    TEXT NOT NULL CHECK (kind IN ('calls','implements','embeds','imports')),
    PRIMARY KEY (src_id, dst_id, kind)
);
