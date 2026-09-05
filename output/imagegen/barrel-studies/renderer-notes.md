# Frozen canvas barrel studies

[Open the comparison](renderer.html). The approved concepts appear on the left; live canvas drawings appear on the right. The page paints once, with no animation clock, input handling or audio.

The canvas path uses the game's `drawOcean`, with opt-in water and character renderers. It draws the shaded body and curling lip behind the surfer, then the transparent curtain in front. Shapes, shadows, the character and foam are computed in code; reference images are only displayed alongside the canvases.

```text
test-harness/barrel-studies.ts
  createBarrelStudy(treatment)
  drawOcean(context, ocean, reducedMotion=true, artwork)
    drawBarrelSetting
    drawBarrelBack
    drawSurferModel
      drawBoardTrail
    drawBarrelFront
```

All studies share the same camera and texture seed. The setting uses a blue sky, distant island, quiet surface ripples and a submerged foreground. A has a nearly level crest and long shadow; B has an arched roof with a rounded shaded interior. C keeps a left-side fall while its surfer faces right and its wake trails left.

The surfer has a wider, bent-knee stance, forward-leaning torso, shaded clothes, face and hair, and a cream board with a visible rail and fin. Board-local joints use the ocean's heading, rail tilt, depth, stance and standing blend. The mesh also supports prone paddling and crossed arms. It is rasterized on the pixel grid at 1.8 times the base model size for this comparison.

For C, the lip is now the source of the geometry. It begins on the right shoulder, stays overhead, then curls down on the left. The underside of that curve is the tube roof, its tip defines the mouth, and its projected landing point defines the impact foam. Reducing `lipFall` shrinks both the overhang and the cavity instead of leaving a pre-cut hollow behind.

The large crest-to-trough ribbons have been removed from C. Small broken marks hug the underside of the one lip. The translucent foreground water also samples the falling segment of that lip, and foam gathers beneath its landing point.

A connected cream-and-mint wake begins at the board's tail, sweeps up the face and fades into scattered foam. It uses the same board-local projection as the surfer, so heading, tilt and depth affect both. The static wake is a posed trajectory; an animated wake will need the board's actual position history. Prone poses have a shorter trail, and carried or submerged boards have none.

`barrelLip` is C's primary curve. `barrelSection` inverts that curve to derive the roof at each horizontal position. `barrelLipContour`, the foreground sheet and `barrelLipImpact` all sample the same lip. `BarrelFrame.peelDirection` records the travel direction, but it does not bend decorative lines into a second wave.

These are fixed art poses, not simulated wave states. The game uses its own animated renderer, where the visible roof, falling edge, tube shadow, occlusion and lip collision follow the same evolving curve. The comparison remains useful as the visual target for each beach.

## Rebuild the standalone page

From the repository root:

```sh
app/node_modules/.pnpm/esbuild@0.25.12/node_modules/esbuild/bin/esbuild \
  app/test-harness/barrel-studies.ts --bundle --format=iife \
  --platform=browser --target=es2020 \
  --outfile=output/imagegen/barrel-studies/renderer.js
```

Open `renderer.html` in a browser. No web server or application daemon is required.

## Checks

The focused study suite checks that C's roof equals the falling lip, the cavity grows with `lipFall`, the foreground sheet stays attached to the lip, and the impact sits below its tip. It also retains deterministic rendering, layer order, pixel alignment, character posture, board-tail alignment, wake fading and isolation from normal gameplay.

The tests do not automate or record gameplay. Visual acceptance remains with the player.
