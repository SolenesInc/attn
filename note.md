Views (behind `views_enabled`) now take answers. The question parts work inside `::::each` and `::::one-by-one`: digits pick, `e` writes, and keeping or skipping a one-by-one item is an answer too. The home keeps each answer as a draft at once, so a check survives app and daemon restarts and resumes on the first item left. The send key, or Send, writes `<view>.responses.json` beside the view (on an outpost for an outpost's view) and types one summary line into the agent that opened it, through the same inbox path an ask's answer takes. A view tile no longer shows the annotation controls.

Trade-off: drafts stay after a send, so Send again sends every answer, and each answer pushes the whole tile content again. That is cheap for a hand-sized view, but it is worth revisiting if views grow large values.

![Answering a view by keys, with an app restart mid-way](https://land-feed.caketroll.cloud/m/SolenesInc/attn/e85deb4a6f03c08890fb1584650b5b019617823e0daf802c8ef13a74ad78628c.mp4)

![view-answers-sent](https://land-feed.caketroll.cloud/m/SolenesInc/attn/06d729ea680923c4fafc28826c07b5f78298ed688e5ddd3035c01bf0ac2ced01.png)
