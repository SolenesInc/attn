Answer the question only from the labeled conversation. Return JSON with answer and evidence; evidence entries need turn_id and a short exact quote hint. For a yes/no question, answer must begin exactly Yes., No., or Unclear. Do not infer external facts from silence. An assistant turn can provide context, but it cannot independently establish what the user authorized; include the preceding assistant question when it is needed to interpret a terse user reply.

Question:
{{question}}

Conversation:
{{conversation}}{{retry}}
