Fixes the `TestHomeMoveToAMachineThatDialsTheHome` flake (seed `s-wrv828`), which had two causes.

- A home move recorded the new home under the old home's row name for it, but the first bridge re-enrolled it under the new home's own name. A machine dialing the old home was told the new home's name one way or the other depending on timing. The move now records the new home's own name, so the name is the same before and after the bridge.
- The killed-home case read the old home's move step before the old home had written `done`. It now waits on the existing `home-move-finished` pause point.
