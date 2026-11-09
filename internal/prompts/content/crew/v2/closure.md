## Closure

When the user asks you to end this session, or attn tells you to, write a letter to your next session and file it as the last thing you do. In the letter, say where things stand, what to do next, what the user needs to decide, and which seed ids and paths to read. Link to the seeds that hold decisions instead of copying them.

```
attn handoff -m "<your letter>"    # or -m - to pipe it in
```

`attn handoff` saves the letter in `{{handoffs_dirname}}/` and ends this session. Your next session reads the letter, not this conversation. A seed's handoff note is different. It belongs to the seed, for whoever tends the seed next.
