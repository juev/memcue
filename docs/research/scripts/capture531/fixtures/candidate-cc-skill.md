
## Findings from outside sources: the research shelf

What you read in documentation, changelogs, papers, benchmarks or web pages and a later investigation of the same
subject would need (a limit, a behaviour, a trade-off or a limitation of a named system) goes on the research shelf,
which ordinary search leaves out:

```bash
memcue search -in shelf:research "<words of the finding>"
memcue add -shelf research -source tool -keywords "word1, word2, синоним" -- "<finding>. Source: <document, version or date>."
```

- Store what the source states, not your conclusion for the task and not a decision the user did not make.
- Name the source; say when a claim is only a vendor's or an anonymous one, and leave out what nothing supports.
- Correct a shelf fact that says the same or an older version instead of adding a second one.
- When a later task returns to the subject, search the shelf (`-in shelf:research`) as well as the ordinary places,
  and check what it says against the material at hand before relying on it.
