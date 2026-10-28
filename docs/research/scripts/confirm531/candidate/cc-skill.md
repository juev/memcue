
## Findings from outside sources: the research shelf

What you read in a web page, another project's documentation, a changelog, a paper or a benchmark that is not part of
the repository, and a later investigation of the same subject would need (a limit, a behaviour, a trade-off or a
limitation of a named system), goes on the research shelf, which ordinary search leaves out:

```bash
memcue search -in shelf:research "<words of the finding>"
memcue add -shelf research -source tool -keywords "word1, word2, синоним" -- "<finding>. Source: <document, version or date>."
```

- Store what your conclusion rests on and its limits, not every detail of the page.
- Store what the source states, not your conclusion for the task and not a decision the user did not make.
- Name the source; say when a claim is only a vendor's or an anonymous one, and leave out what nothing supports.
- A document in the repository is what the repository shows: do not store it. A pitfall, a host or a fact of this
  repository goes where it goes today, not on the shelf.
- Correct a shelf fact that says the same or an older version instead of adding a second one.
- When a later task returns to the subject, search the shelf (`-in shelf:research`) as well as the ordinary places.
  Before relying on a shelf fact, compare its version or date with what the workspace uses. An answer built on it
  says no more than the fact and its source state, and marks an inference as one.
