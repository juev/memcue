# Facts that name a file, at the moment of a Read: the Mac stage, November 2026 (#598)

Status: **measured 2026-11-03 on the author's Mac, the interactive corpus #598 names; the idea is dropped.** Follows the
Linux first stage of [file-context-598](file-context-598-2026-11.md) with its script `file598.py measure`, run
unchanged on a copy of the live base and on the Mac's Claude Code transcripts. No model was called.

In short:

- **A Read touches a file a fact named at the time in 38 of 5,274 calls (0.7%), in 26 of 456 sessions.** 76 more
  calls match only through a version whose scope may have changed later (ambiguous). Even counting those, 2% of reads.
- **The facts are few too**: 418 of the 3,081 active facts name a file, 220 by an exact path; 40 of them were ever
  matched by a Read.
- **Measured by #598's own rule, that is rare**: the method stops here and does not run the A/B with a model.
- **Cost would be small**: matching takes about 0.2 ms a path over an index built in 0.8 s, and the matched facts add
  about 1,000 characters to a session that has any. Too few sessions would get them to be worth a hook.
- **Recommendation: drop the file hook.** A fact that names a file still reaches the agent through its own search.

## What was measured

`file598.py measure` over a copy of the text history taken at 2026-11-03T09:33Z and every Claude Code transcript on the
Mac (1,513 files: 580 main sessions, 108 subagent sessions, this session left out), 2026-10-01 to 2026-11-03.

- **Facts**: the paths a fact's text names in an unambiguous shape: an absolute path, a `~/` path, a path relative to
  the root of a checkout of the fact's project. A directory, a bare file name and a relative path under another root
  count only as loose matches.
- **Calls**: every `Read` of the transcripts, and every path a `Bash` command names, as a second population.
- **as of**: a call counts when every version of the fact that may have been in force at the call names its path.
  A Relabel or Retag keeps the time of the version before, so a match that depends on such a change is counted apart
  as ambiguous.

## Results

| calls | all | matched as of the call | sessions | facts | ambiguous only |
|---|---|---|---|---|---|
| Read | 5,274 in 456 sessions | **38** (by home path 21, relative 17, absolute 1) | 26 | 40 | 76 |
| Bash paths | 60,882 in 580 sessions | 904 | 120 | 237 | 1,636 |

Against the end of the day of the measurement, ignoring when each call was made (an upper bound), 181 Read calls in 86
sessions would match.

The Linux first stage counted 6 of 591 Claude Code reads (1%) and 77 of 2,218 Hermes reads (3.5%); the Mac's
interactive corpus, the one #598 asked for, is no denser.

## Questions of #598

1. **How many facts name a file, and how many of them does a Read find?** 418 of 3,081 name one, 220 by an exact path;
   a Read found 40 of them in a month.
2. **How often does a Read touch such a file?** 0.7% of reads, 6% of sessions.
3. **Do answers get better with them?** Not run: by the method, a rate this low ends the study before the A/B.
4. **What would it cost?** About 0.2 ms a Read and about 1,000 characters for a session that gets any.

## Limits

- One month of one person's sessions; a project with many path-heavy facts could be denser.
- Paths are matched by shape; a fact that names a file in prose ("the release script") is not counted.
- Bash paths match more often (904 calls); a hook on Bash is a different question from #598's hook on Read.
