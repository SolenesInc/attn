Signup can let people in before they confirm their email, or hold them until they do.

```mermaid
flowchart LR
  A[Sign up] --> B{Confirm first?}
  B -- yes --> C[Wait for email] --> D[First login]
  B -- no --> D --> E[Reminder after 7 days]
```

:::question
Should new accounts confirm their email before the first login?
Holding them cuts spam signups, but about one in five never finds the email.
- [ ] Confirm later — no lost signups, reminder after 7 days
- [ ] Confirm first — fewer spam accounts, some signups lost
Recommended: Confirm later
:::
