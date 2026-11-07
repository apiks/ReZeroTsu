# ZeroTsu

<p align="center">
	<img src="https://images-wixmp-ed30a86b8c4ca887773594c2.wixmp.com/f/6e4868e2-f52b-4c7d-a984-d5027576b221/dch684c-818cbf96-b76b-4e75-8445-75d1497195b7.png?token=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1cm46YXBwOjdlMGQxODg5ODIyNjQzNzNhNWYwZDQxNWVhMGQyNmUwIiwiaXNzIjoidXJuOmFwcDo3ZTBkMTg4OTgyMjY0MzczYTVmMGQ0MTVlYTBkMjZlMCIsIm9iaiI6W1t7InBhdGgiOiJcL2ZcLzZlNDg2OGUyLWY1MmItNGM3ZC1hOTg0LWQ1MDI3NTc2YjIyMVwvZGNoNjg0Yy04MThjYmY5Ni1iNzZiLTRlNzUtODQ0NS03NWQxNDk3MTk1YjcucG5nIn1dXSwiYXVkIjpbInVybjpzZXJ2aWNlOmZpbGUuZG93bmxvYWQiXX0.w_Pmn6zmDv4NcB9h-lPko3-7qnvGmLqVD7862q59XR8" alt="Zero Two" width="300" height="300">
</p>

Anime episode alerts, airing schedules and a few server tools, in one Discord bot. Airing data comes from [AnimeSchedule.net](https://animeschedule.net).

## What it does

**Never miss an episode.** Subscribe to a show and get a DM the moment a new episode airs. Subbed, dubbed or raw, your call. Follow a show before it premieres and the bot tells you when it starts.

**An anime channel that runs itself.** Post every new episode to a channel with a role ping, and the day's schedule at the start of each day. Pick sub, dub or raw once in `/settings` and both posts follow it.

**The week at a glance.** `/schedule` shows what airs today, with a day picker across three weeks. Works in servers, in DMs and as a user install.

**Names your members know.** Show titles in Romaji, English or Native. Servers pick for their channels, members pick for their DMs.

**Roles without the admin work.** Reaction roles on any message, and voice channel roles that come and go as members join and leave.

**Raffles.** Members enter by command or by reacting, and one command draws the winner.

**The small stuff.** DM reminders, `/say` to post as the bot and edit it later, `/prune`, `/roll`, `/pick`, `/joke`, `/avatar`.

Everything is a slash command. `/help` lists them by category. Mod commands default to Manage Server, and admins can hand them to other roles under Server Settings → Integrations.

## Support

Questions, bugs, ideas: the [support server](https://discord.gg/BDT8Twv), or an issue here. To invite the bot, run `/invite` in any server that already has it.

If the bot saves you time, [support it on Patreon](https://patreon.com/animeschedule).

ZeroTsu is the successor to [the original ZeroTsu](https://github.com/apiks/ZeroTsu). MIT licensed.

## Setup & Run

You need Go 1.27+, MongoDB and a bot token from the [Discord Developer Portal](https://discord.com/developers/applications).

1. Set the `ZeroTsuToken` environment variable to your bot token.
2. Copy `config.json.example` to `config.json` and fill in `mongo_uri`, `owner_id`, `prefixes` and `user_agent`. The anime features need an `anime_schedule_api_key` from [AnimeSchedule.net](https://animeschedule.net); leave it empty to turn them off. The remaining keys are optional and the example file explains them.
3. `go build -o zerotsu` and run the binary from the directory that holds `config.json`.

Sharding is automatic. Run one instance per database.
