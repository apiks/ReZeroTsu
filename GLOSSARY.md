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
A user's request for a DM whenever a new episode of one show airs on any of its subscription tracks.
_Avoid_: sub, guild subscription, channel subscription

**Subscription tracks**:
The tracks a subscription alerts for: Sub, Dub, Raw, Sub and Dub, or every track. Sub is the default. Inside the set, Sub means the sub track, or the raw track in a week the show has no sub airing; Dub and Raw mean exactly that track.
_Avoid_: track filter, subscription type, language option

**Preferred track**:
The one track a server, or one run of the schedule command, wants shown: Sub, Dub or Raw. Sub is the default. Each show appears once per airing, as its preferred track when it has one that week, else down the chain: Sub falls back to raw and never shows dub; Dub falls back to sub, then raw; Raw shows raw only.
_Avoid_: track choice, track setting, air-type preference, "All"

**Timetable**:
AnimeSchedule's week-by-week list of episode air times. The bot holds last week's, this week's and next week's, with days counted in UTC.
_Avoid_: schedule data, cache

**Episode span**:
Several episodes of one show released together on one track at one time, named by their first and last episode, as in Ep 1-12. A release is a span only when its first episode is above zero and below its last.
_Avoid_: batch, episode range, multi-episode

**Upcoming show**:
A show listed on AnimeSchedule whose first episode has not aired yet. It can be subscribed to before it reaches the timetable.
_Avoid_: unaired show, announced anime

**Donghua**:
Chinese animation, of any media type. A server can hide it from its schedule pages and from the shows offered for a new subscription.
_Avoid_: Chinese anime, ONA (Chinese)

**Track**:
An independent airing stream of a show: raw, sub or dub. A timetable entry belongs to one track. A show always has a raw track; a sub or dub track exists only when AnimeSchedule lists it, and a show can have one without an announced premiere for it.
_Avoid_: air type, airing type, version, language

**Daily-schedule autopost**:
A server's standing order to post each UTC day's timetable page to one channel at the start of that day.
_Avoid_: daily post, schedule feed

**New-episodes autopost**:
A server's standing order to post every newly aired episode to one channel, optionally pinging a role. It is never called a subscription.
_Avoid_: episode feed, server subscription

### Reddit

**Reddit feed**:
A server's standing order to post new posts from one subreddit, in one sort, to one channel, optionally narrowed to one author or a title prefix. Its channel, subreddit, sort, author and title identify it; auto-pin is a setting of the feed, not part of its identity.
_Avoid_: subreddit feed, reddit subscription, post type feed

**Sort**:
Which of a subreddit's listings a Reddit feed follows: hot, rising or new.
_Avoid_: post type, feed type

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
