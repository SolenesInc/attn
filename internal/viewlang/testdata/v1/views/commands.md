:::data run="scripts/main-status.sh" every="30s"
:::

:::commands
rerun: scripts/rerun.sh {id}
stop: confirm="Stop {name}?" scripts/stop.sh --job={id}
sync: scripts/sync.sh
:::

:::controls
sync
:::

:::list ci.jobs
title: {name}
press: rerun
controls: rerun, stop
:::
