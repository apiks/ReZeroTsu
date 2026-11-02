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
