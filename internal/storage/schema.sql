CREATE TABLE accounts (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    email           TEXT NOT NULL UNIQUE,
    imap_host       TEXT NOT NULL,
    imap_port       INTEGER NOT NULL,
    imap_username   TEXT NOT NULL,
    use_tls         INTEGER NOT NULL,
    color           TEXT NOT NULL,
    created_at      INTEGER NOT NULL
);

CREATE TABLE folders (
    id              INTEGER PRIMARY KEY,
    account_id      INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    delimiter       TEXT NOT NULL,
    role            TEXT,
    uid_validity    INTEGER NOT NULL,
    uid_next        INTEGER NOT NULL,
    last_synced_at  INTEGER,
    UNIQUE(account_id, name)
);

CREATE TABLE messages (
    id              INTEGER PRIMARY KEY,
    account_id      INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id       INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    uid             INTEGER NOT NULL,
    message_id      TEXT,
    in_reply_to     TEXT,
    references_     TEXT,
    thread_id       INTEGER REFERENCES threads(id) ON DELETE SET NULL,
    subject         TEXT,
    from_addr       TEXT,
    to_addrs        TEXT,
    cc_addrs        TEXT,
    date            INTEGER NOT NULL,
    flags           TEXT NOT NULL,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    body_text       TEXT,
    body_html       TEXT,
    UNIQUE(account_id, folder_id, uid)
);
CREATE INDEX idx_messages_thread ON messages(thread_id);
CREATE INDEX idx_messages_date   ON messages(date DESC);
CREATE INDEX idx_messages_msgid  ON messages(message_id);

CREATE TABLE threads (
    id              INTEGER PRIMARY KEY,
    subject_norm    TEXT NOT NULL,
    last_date       INTEGER NOT NULL,
    msg_count       INTEGER NOT NULL DEFAULT 0,
    unread_count    INTEGER NOT NULL DEFAULT 0,
    has_flagged     INTEGER NOT NULL DEFAULT 0,
    has_attach      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_threads_last_date ON threads(last_date DESC);

CREATE TABLE attachments (
    id              INTEGER PRIMARY KEY,
    message_id      INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    part_id         TEXT NOT NULL,
    filename        TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    size_bytes      INTEGER NOT NULL,
    sha256          TEXT,
    local_path      TEXT,
    downloaded_at   INTEGER
);
CREATE INDEX idx_attachments_msg ON attachments(message_id);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    subject, from_addr, to_addrs, body_text,
    content='messages', content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, subject, from_addr, to_addrs, body_text)
    VALUES (new.id, new.subject, new.from_addr, new.to_addrs, new.body_text);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_addr, to_addrs, body_text)
    VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addrs, old.body_text);
END;
CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_addr, to_addrs, body_text)
    VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addrs, old.body_text);
    INSERT INTO messages_fts(rowid, subject, from_addr, to_addrs, body_text)
    VALUES (new.id, new.subject, new.from_addr, new.to_addrs, new.body_text);
END;

CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);

INSERT INTO schema_migrations(version, applied_at) VALUES (1, strftime('%s','now'));
