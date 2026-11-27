:::data run="scripts/main-status.sh" every="30s"
:::

:::facts
Head | `{head.sha}` {head.subject} · {head.at|ago}
Landed today | {landed}
:::
