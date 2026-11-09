\set ON_ERROR_STOP 1
SET http.timeout_msec = 120000;
CREATE SCHEMA IF NOT EXISTS bench;
-- Embeds texts through a TEI endpoint, in input order.
CREATE OR REPLACE FUNCTION bench.embed(url text, texts text[]) RETURNS TABLE(ord bigint, v vector) LANGUAGE sql AS $$
  SELECT e.ord, e.value::text::vector
  FROM http_post(url, jsonb_build_object('inputs', to_jsonb(texts), 'truncate', true)::text, 'application/json') r,
       jsonb_array_elements(r.content::jsonb) WITH ORDINALITY AS e(value, ord)
$$;
CREATE TABLE IF NOT EXISTS bench.emb_e5 (id text PRIMARY KEY, v vector(384) NOT NULL);
CREATE TABLE IF NOT EXISTS bench.emb_bge (id text PRIMARY KEY, v vector(1024) NOT NULL);
-- Fills one model's vectors for every active fact, batch by batch.
CREATE OR REPLACE PROCEDURE bench.fill(model text, batch int) LANGUAGE plpgsql AS $$
DECLARE ids text[]; texts text[]; url text; prefix text;
BEGIN
  PERFORM set_config('http.timeout_msec', '120000', false);
  url := CASE model WHEN 'e5' THEN 'http://memcue-682-tei-e5/embed' ELSE 'http://memcue-682-tei-bge/embed' END;
  prefix := CASE model WHEN 'e5' THEN 'passage: ' ELSE '' END;
  LOOP
    EXECUTE format('SELECT array_agg(id ORDER BY id), array_agg(%L || (payload->>''Content'') ORDER BY id) FROM (
      SELECT f.id, f.payload FROM memcue.facts f WHERE f.active AND NOT EXISTS (SELECT 1 FROM bench.%I x WHERE x.id = f.id) ORDER BY f.id LIMIT %s) s',
      prefix, 'emb_' || model, batch) INTO ids, texts;
    EXIT WHEN ids IS NULL;
    EXECUTE format('INSERT INTO bench.%I SELECT ids[e.ord], e.v FROM bench.embed($1, $2) e, (SELECT $3::text[] AS ids) k', 'emb_' || model) USING url, texts, ids;
    COMMIT;
  END LOOP;
END $$;
