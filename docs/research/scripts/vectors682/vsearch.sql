\set ON_ERROR_STOP 1
CREATE TABLE IF NOT EXISTS bench.prompts (qid text PRIMARY KEY, prompt text NOT NULL);
-- One vector search: the query is embedded through TEI by PostgreSQL, then ranked by cosine over the active facts
-- that search -in all sees (no shelf, not expired). Times are taken inside the server.
CREATE OR REPLACE FUNCTION bench.vsearch(model text, qtext text, k int, today text)
RETURNS TABLE(rank bigint, id text, cos float8, embed_ms float8, search_ms float8) LANGUAGE plpgsql AS $$
DECLARE t0 timestamptz; t1 timestamptz; qv vector; url text; prefix text;
BEGIN
  PERFORM set_config('http.timeout_msec', '60000', true);
  url := CASE model WHEN 'e5' THEN 'http://memcue-682-tei-e5/embed' ELSE 'http://memcue-682-tei-bge/embed' END;
  prefix := CASE model WHEN 'e5' THEN 'query: ' ELSE '' END;
  t0 := clock_timestamp();
  SELECT e.v INTO qv FROM bench.embed(url, ARRAY[prefix || qtext]) e;
  t1 := clock_timestamp();
  RETURN QUERY EXECUTE format('SELECT row_number() OVER (ORDER BY d), x.id, 1 - d, %s::float8, (extract(epoch FROM clock_timestamp() - %L::timestamptz) * 1000)::float8
    FROM (SELECT e.id, e.v <=> $1 AS d FROM bench.%I e JOIN memcue.facts f USING (id)
          WHERE f.active AND (f.expires = '''' OR f.expires >= $2) AND f.scope_fold NOT LIKE ''shelf:%%''
          ORDER BY e.v <=> $1 LIMIT $3) x', extract(epoch FROM t1 - t0) * 1000, t1, 'emb_' || model) USING qv, today, k;
END $$;
