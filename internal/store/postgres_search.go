package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/juev/memcue/internal/text"
)

// tsquery is the PostgreSQL full-text query of one search term: the stem as it is, or a prefix of it for a long
// Russian term (text.Prefix). The lexemes of memcue.facts are the stems themselves, with no dictionary, so the
// quoted form keeps the server from normalizing the term.
func tsquery(term string) (lexeme string, prefix bool, query string) {
	p, ok := text.Prefix(term)
	q := "'" + strings.ReplaceAll(strings.ReplaceAll(p, `\`, `\\`), "'", "''") + "'"
	if ok {
		q += ":*"
	}
	return p, ok, q
}

// pgArgs numbers the parameters of a statement as it is assembled.
type pgArgs []any

func (a *pgArgs) add(v any) string {
	*a = append(*a, v)
	return fmt.Sprintf("$%d", len(*a))
}

// remoteWhere is Store.where over memcue.facts f: the same conditions, on the columns PostgreSQL derives from the
// payload. A scope compares as SQLite's lower() folds it, ASCII only, against the argument folded by strings.ToLower.
func remoteWhere(f Filter, a *pgArgs) string {
	var conds []string
	if len(f.Kinds) > 0 {
		conds = append(conds, "f.kind = ANY("+a.add(f.Kinds)+"::text[])")
	}
	if f.Since != "" {
		conds = append(conds, "left(f.created, 19) >= "+a.add(f.Since))
	}
	if f.Today != "" {
		conds = append(conds, "(f.expires = '' OR f.expires >= "+a.add(f.Today)+")")
	}
	if len(f.Scopes)+len(f.ScopePrefixes)+len(f.Origins) > 0 {
		var or []string
		for _, sc := range f.Scopes {
			or = append(or, "f.scope_fold = "+a.add(strings.ToLower(sc)))
		}
		for _, p := range f.ScopePrefixes {
			or = append(or, fmt.Sprintf("left(f.scope_fold, %s) = %s", a.add(utf8.RuneCountInString(p)), a.add(strings.ToLower(p))))
		}
		for _, o := range f.Origins {
			origin := fmt.Sprintf("left(f.origin, %s) = %s", a.add(utf8.RuneCountInString(o)+1), a.add(o+" "))
			if f.OriginOnlyUnscoped {
				origin = "(f.scope_fold = '' AND " + origin + ")"
			}
			or = append(or, origin)
		}
		conds = append(conds, "("+strings.Join(or, " OR ")+")")
	}
	for _, p := range f.ExcludePrefixes {
		cond := fmt.Sprintf("left(f.scope_fold, %s) <> %s", a.add(utf8.RuneCountInString(p)), a.add(strings.ToLower(p)))
		if len(f.KeepScopes) > 0 {
			keep := make([]string, len(f.KeepScopes))
			for i, sc := range f.KeepScopes {
				keep[i] = strings.ToLower(sc)
			}
			cond = "(" + cond + " OR f.scope_fold = ANY(" + a.add(keep) + "::text[]))"
		}
		conds = append(conds, cond)
	}
	if len(conds) == 0 {
		return ""
	}
	return " AND " + strings.Join(conds, " AND ")
}

// remoteSearch ranks the active facts that hold the terms by BM25 on the server, with the parameters and the IDF of
// SQLite FTS5 (k1 1.2, b 0.75; the count and average length over all active facts), and returns the score negated
// as FTS5 reports it. The GIN index finds the candidates; their term frequencies come from the stored terms, so a
// prefix term counts every stem it starts. Ties go by id. The order can differ from the local base where FTS5 breaks
// ties by its own row order or rounds a sum differently.
func remoteSearch(s *Store, terms []string, limit int, f Filter) ([]Hit, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	lexemes := make([]string, len(terms))
	prefixes := make([]bool, len(terms))
	queries := make([]string, len(terms))
	for i, t := range terms {
		lexemes[i], prefixes[i], queries[i] = tsquery(t)
	}
	join := " | "
	if f.AllTerms {
		join = " & "
	}
	var a pgArgs
	q := `WITH q AS (
  SELECT u.ord, u.lex, u.pre, u.tq::tsquery AS tq
  FROM unnest(` + a.add(lexemes) + `::text[], ` + a.add(prefixes) + `::boolean[], ` + a.add(queries) + `::text[]) WITH ORDINALITY AS u(lex, pre, tq, ord)
), st AS (
  SELECT count(*)::float8 AS n, coalesce(avg(length), 0)::float8 AS avgdl FROM memcue.facts WHERE active
), w AS (
  SELECT q.ord, q.lex, q.pre, greatest(ln((st.n - d.df + 0.5) / (d.df + 0.5)), 1e-6) AS idf
  FROM q CROSS JOIN st CROSS JOIN LATERAL (
    SELECT count(*)::float8 AS df FROM memcue.facts c WHERE c.active AND c.lexemes @@ q.tq) d
), cand AS MATERIALIZED (
  SELECT f.id, f.version, f.payload, f.terms, f.length FROM memcue.facts f
  WHERE f.active AND f.lexemes @@ ` + a.add(strings.Join(queries, join)) + `::tsquery` + remoteWhere(f, &a) + `
), tf AS (
  SELECT c.id, w.ord, count(*)::float8 AS tf
  FROM cand c CROSS JOIN LATERAL unnest(c.terms) AS x(lex) JOIN w ON NOT w.pre AND x.lex = w.lex
  GROUP BY c.id, w.ord
  UNION ALL
  SELECT c.id, w.ord, count(*)::float8
  FROM cand c CROSS JOIN LATERAL unnest(c.terms) AS x(lex) JOIN w ON w.pre AND starts_with(x.lex, w.lex)
  GROUP BY c.id, w.ord
), score AS (
  SELECT tf.id, -sum(w.idf * tf.tf * 2.2 / (tf.tf + 1.2 * (0.25 + 0.75 * c.length / greatest(st.avgdl, 1e-9)))) AS bm25
  FROM tf JOIN w USING (ord) JOIN cand c USING (id) CROSS JOIN st
  GROUP BY tf.id
)
SELECT c.version, c.payload, c.terms, s.bm25 FROM score s JOIN cand c USING (id)
ORDER BY s.bm25, c.id LIMIT ` + a.add(limit)
	return remoteRead(s, func(ctx context.Context, c *pgx.Conn) ([]Hit, error) {
		rows, err := c.Query(ctx, q, a...)
		if err != nil {
			return nil, errRemote
		}
		defer rows.Close()
		var out []Hit
		for rows.Next() {
			var h Hit
			fact, err := scanRemote(ctx, rows, &h.Stems, &h.BM25)
			if err != nil {
				return nil, errRemote
			}
			h.Fact = fact
			out = append(out, h)
		}
		if rows.Err() != nil {
			return nil, errRemote
		}
		return out, nil
	})
}

// remoteDocFreq counts, in one statement, the active facts that hold each term and all the active facts.
func remoteDocFreq(s *Store, terms []string) (map[string]int, int, error) {
	queries := make([]string, len(terms))
	for i, t := range terms {
		_, _, queries[i] = tsquery(t)
	}
	type result struct {
		total  int64
		counts []int64
	}
	r, err := remoteRead(s, func(ctx context.Context, c *pgx.Conn) (result, error) {
		var r result
		err := c.QueryRow(ctx, `SELECT (SELECT count(*) FROM memcue.facts WHERE active),
  coalesce((SELECT array_agg(d.n ORDER BY u.ord) FROM unnest($1::text[]) WITH ORDINALITY AS u(tq, ord)
    CROSS JOIN LATERAL (SELECT count(*) AS n FROM memcue.facts c WHERE c.active AND c.lexemes @@ u.tq::tsquery) d), '{}')`,
			queries).Scan(&r.total, &r.counts)
		if err != nil || len(r.counts) != len(terms) {
			return r, errRemote
		}
		return r, nil
	})
	if err != nil {
		return nil, 0, err
	}
	df := make(map[string]int, len(terms))
	for i, t := range terms {
		df[t] = int(r.counts[i])
	}
	return df, int(r.total), nil
}

func remoteRepoScopes(s *Store) ([]string, error) {
	out, err := remoteStrings(s, `SELECT DISTINCT scope FROM memcue.facts WHERE active AND scope_fold LIKE 'repo:%'`)
	sort.Strings(out)
	return out, err
}

func remoteOriginTiers(s *Store) ([]string, error) {
	out, err := remoteStrings(s, `SELECT DISTINCT split_part(origin, ' ', 1) FROM memcue.facts WHERE active AND position(' ' in origin) > 1`)
	sort.Strings(out)
	return out, err
}

func remoteCountNamed(s *Store, name string) (int, error) {
	return remoteRead(s, func(ctx context.Context, c *pgx.Conn) (int, error) {
		var n int
		err := c.QueryRow(ctx, `SELECT count(*) FROM memcue.facts WHERE active
  AND ((char_length(origin) = char_length($1) + 11 AND left(origin, char_length($1) + 1) = $1 || ' ') OR scope = 'repo:' || $1)`, name).Scan(&n)
		if err != nil {
			return 0, errRemote
		}
		return n, nil
	})
}
