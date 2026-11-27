The app on a machine that joined its home with `attn enrollment join` now works against the home. Its daemon relays the app's WebSocket to the home over the reverse bridge, and the home trusts it with the machine's own credentials. `attn`, `/mcp` and `/health` on that machine stay local.

While the home is unreachable, the app shows the home's name and the reason, offers Retry (R or Return), and connects by itself when the home answers. Until that machine serves its own terminals (s-kt444p), the home starts no terminals there. The launcher leaves it out, and spawn, `attn terminal open --host` and `attn delegate --host` refuse and name it.

![Can't reach the home](https://land-feed.caketroll.cloud/m/SolenesInc/attn/7d283c9b5e43d7603d901c44a3debbbaf5f13ffa696db8fc3078afc699b61818.png)
