On a machine whose home refuses its newer build, the window now shows one band: the update band ("oma runs another build." with Update and both versions). Before, a second band stacked above it with the raw refusal, "oma refused this machine: daemon d-… runs build …". That is what every Mac update showed until the automatic Update finished. `home_unreachable` gains an optional `refusal` ("build") so the app hides only a build refusal, and only while the update band speaks for the home. Other refusals keep their band. The raw words stay in `outpost_last_error` and the daemon log, and come back in the band once the update band is dismissed.

Before:
![before](https://land-feed.caketroll.cloud/m/SolenesInc/attn/9b71d45f5ee1b32b2f936c5001959f207ac3b601e15be292ae30e4c5d4bbe839.png)

After:
![after](https://land-feed.caketroll.cloud/m/SolenesInc/attn/1fdfd76b1bbe4b1454ad982e364c968d6217fde5a102f0cf416f3adf35cdd87f.png)
