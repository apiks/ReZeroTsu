# ReZeroTsu

A Discord bot that posts anime episode notifications, daily schedules, Reddit feeds and reminders to servers and users, each at most once.

## Language

### Delivery

**Delivery run**:
One periodic pass of a notification job (new episodes, episode DMs, daily schedule, feeds, reminders) over its targets.
_Avoid_: loop, job tick, check

**Send rules**:
The shared policy every delivery run follows when posting: never send after shutdown starts, wait for the shared send budget at the run's priority before sending, fall back once from a rejected webhook to the bot account, and verify an outcome Discord left unknown before deciding whether it landed.
_Avoid_: send structure, delivery rules

**Channel sender**:
The one way a delivery run posts its messages to a single channel or DM under the send rules; whether a message goes through a webhook or the bot account is its concern alone.
_Avoid_: channel transport, poster, delivery port

### Servers

**Moderator**:
A server member allowed to run the bot's mod commands: anyone with Manage Server, plus whoever the server's admins grant those commands to through Discord's own command permissions.
_Avoid_: mod role, command role, moderator role

### Anime

**Subscription**:
A user's request for a DM whenever a new episode of one show airs.
_Avoid_: sub, guild subscription, channel subscription

**New-episodes autopost**:
A server's standing order to post every newly aired episode to one channel, optionally pinging a role. It is never called a subscription.
_Avoid_: episode feed, server subscription

### Raffles

**Raffle**:
A named giveaway in one server that members enter and that a moderator draws winners from.
_Avoid_: giveaway, lottery

**Entrant**:
A member who has entered a raffle.
_Avoid_: participant

**Draw**:
Choosing one entrant of a raffle at random as its winner. A raffle stays open after a draw and can be drawn again.
_Avoid_: pick, roll

### Reminders

**Reminder**:
A message a user asks the bot to send back to them at a chosen time. It is only ever delivered by DM.
_Avoid_: remindme
