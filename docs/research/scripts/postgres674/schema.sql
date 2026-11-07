-- Disposable research database only. No production migration.
CREATE EXTENSION vector;
CREATE TABLE facts (
 id text NOT NULL, version integer NOT NULL CHECK (version > 0), content text NOT NULL,
 origin text NOT NULL DEFAULT '', created text NOT NULL,
 status text NOT NULL CHECK (status IN ('active','retired')),
 author text NOT NULL DEFAULT '', expires text NOT NULL DEFAULT '', kind text NOT NULL DEFAULT '',
 scope text NOT NULL DEFAULT '', source text NOT NULL DEFAULT '', evidence text NOT NULL DEFAULT '',
 review text NOT NULL DEFAULT '', prompt_version integer NOT NULL DEFAULT 0, channel text NOT NULL DEFAULT '',
 PRIMARY KEY(id,version)
);
CREATE INDEX facts_created ON facts(created);
-- Latest active versions only. Expiry remains request-time, as in SQLite.
CREATE TABLE fact_index (
 rid bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, id text NOT NULL UNIQUE, version integer NOT NULL,
 stems text NOT NULL, numbers integer NOT NULL DEFAULT 2,
 FOREIGN KEY(id,version) REFERENCES facts(id,version)
);
CREATE INDEX fact_index_numbers ON fact_index(numbers);
-- PostgreSQL equivalent of the SQLite external-content FTS5 virtual table.
CREATE TABLE fact_fts (rid bigint PRIMARY KEY REFERENCES fact_index ON DELETE CASCADE, lexemes tsvector NOT NULL);
CREATE INDEX fact_fts_lexemes ON fact_fts USING gin(lexemes);
CREATE TABLE fact_vectors (
 id text NOT NULL, tag text NOT NULL, dim integer NOT NULL CHECK(dim=384), text_hash text NOT NULL,
 version integer NOT NULL, vec vector(384) NOT NULL, PRIMARY KEY(id,tag),
 FOREIGN KEY(id,version) REFERENCES facts(id,version)
);
CREATE TABLE stamps (name text PRIMARY KEY, value text NOT NULL);
-- Technical metadata. Durable replies commit with the fact and derived indexes.
CREATE TABLE operations (
 principal text NOT NULL, key text NOT NULL, request_hash text NOT NULL, response jsonb NOT NULL,
 PRIMARY KEY(principal,key)
);
