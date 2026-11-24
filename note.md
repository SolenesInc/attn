Sigma workers started on an outpost now adopt as visible Attn sessions owned by the home daemon, with the outpost as their host.

- Outpost daemons forward worker adoption to the home over the new home-located `adopt_server_conversation` RPC, receive the session copy row synchronously so caller identity (`SIGMA_ROOT_CONVERSATION`) and linked monitoring resolve immediately, and retry deferred adoptions once the bridge reconnects.
- Close deletion copy rows travel with `agent_launch_close` (`committed`), deleted copy watermarks stay monotonic, and conversation admission, holder lookups, CLI caller resolution, shared-conversation detection, and close release are scoped by host so home and outpost Sigma workers sharing sequential numeric IDs never collide.
