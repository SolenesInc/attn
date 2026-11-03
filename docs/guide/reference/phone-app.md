# The phone app

The attn phone app shows your agents on an iPhone: the queue of agents waiting
for you and the ones working, and the Garden, where you read seeds, add notes
and move them. The phone reaches your computer through Attn Cloud, which relays
end-to-end encrypted traffic it cannot read. Phone access is experimental and
off by default.

## Install the app from TestFlight

The app is in beta and installs through Apple's TestFlight app, not the App
Store.

1. Ask the person who runs attn's App Store Connect account to add you as a
   tester. Give them the email address of your Apple Account.
2. On the iPhone, install **TestFlight** from the App Store.
3. Open the invitation email from TestFlight on the iPhone and tap
   **View in TestFlight**. If you are an internal tester, TestFlight lists the
   app without an email.
4. In TestFlight, tap **attn.codes**, then **Install**. The app shows on the
   Home Screen as **attn**.

TestFlight installs new builds as they come out when **Automatic Updates** is on
for attn.codes. A TestFlight build stops opening 90 days after it was uploaded,
so keep updates on.

## Pair the phone with a profile

A pairing connects the phone to one profile of one computer. The phone sees
that profile's agents and nothing else. Pair once per profile you want to see.
The app merges every pairing into one queue.

1. In attn on your computer, open **Settings → Experimental** and turn on
   **Phone access**. attn connects to Attn Cloud, and a **Phones** page
   appears under **Connectivity**.
2. Open **Settings → Phones**. Under the profile you want, click
   **Pair a phone**. attn shows a QR code. The code expires after 10
   minutes. Click **Show a new pairing code** for a fresh one.
3. On the iPhone, open attn, tap **Pairings**, then **Scan a pairing code**.
   Allow the camera when iOS asks.
4. Point the camera at the QR code. The app returns to **Queue** and lists the
   profile's agents.

## Read and tend the Garden

The **Garden** tab lists the open seeds of every paired profile: plots with how
far their children have come, then the seeds growing, ready, blocked and
parked. Tap a seed to read it: its state and tender, its body, the seeds that
are part of it, its checks and the newest entries of its log. The seed stays
current while you read it: when an agent adds a note or edits the body, the
phone reads it again, and it catches up whenever it connects to the home
again, such as when the app comes back to the foreground.

- To add a note to the log, write it under **Log** and tap **Add note**. The
  note is yours, as one written on the computer is.
- To park, harvest, wither or replant a seed, tap the move, write the reason,
  and confirm. The move works as it does on the computer's Garden board: it
  moves a seed an agent tends, and the log records that you moved it.

Images in a seed's body open on the computer. A seed whose body is larger
than the phone can take in one message says so; read it on the computer.

When the phone cannot reach a profile, the Garden says so at the top, and does
not call a Garden it has not received empty. The profile's seeds stay in the
list as they were at the last connection, greyed and labelled offline,
unreachable or connecting, until the home sends its Garden again.

## Remove a pairing

- To stop a phone reaching a profile, click **Revoke** next to the phone in
  **Settings → Phones**.
- To remove a pairing from the phone only, tap **Forget** on the pairing in the
  app's **Pairings** tab.
- To disconnect every phone at once, turn off **Phone access** in
  **Settings → Experimental**.
