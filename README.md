# ZeroTsu is a Discord BOT with anime and reddit functionalities

<p align="center">
	<img src="https://images-wixmp-ed30a86b8c4ca887773594c2.wixmp.com/f/6e4868e2-f52b-4c7d-a984-d5027576b221/dch684c-818cbf96-b76b-4e75-8445-75d1497195b7.png?token=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1cm46YXBwOjdlMGQxODg5ODIyNjQzNzNhNWYwZDQxNWVhMGQyNmUwIiwiaXNzIjoidXJuOmFwcDo3ZTBkMTg4OTgyMjY0MzczYTVmMGQ0MTVlYTBkMjZlMCIsIm9iaiI6W1t7InBhdGgiOiJcL2ZcLzZlNDg2OGUyLWY1MmItNGM3ZC1hOTg0LWQ1MDI3NTc2YjIyMVwvZGNoNjg0Yy04MThjYmY5Ni1iNzZiLTRlNzUtODQ0NS03NWQxNDk3MTk1YjcucG5nIn1dXSwiYXVkIjpbInVybjpzZXJ2aWNlOmZpbGUuZG93bmxvYWQiXX0.w_Pmn6zmDv4NcB9h-lPko3-7qnvGmLqVD7862q59XR8" alt="Zero Two" width="300" height="300">
</p>

## Features

* **New Anime Episodes feed (subbed).** Optional DM notifications when new episodes release; optional channel autopost with role ping. Source: [AnimeSchedule.net](https://animeschedule.net).

* **Daily Anime Schedule.** View or autopost the daily schedule. Source: [AnimeSchedule.net](https://animeschedule.net).

* **Customizable Reddit RSS autopost.** Post threads by subreddit, sort (hot / rising / new), optional author or title filters, and optional auto-pin of the latest post.

* **Reaction roles.** Give or remove roles using message reactions.

* **Voice channel roles.** Automatically assign roles when users join a voice channel and remove them when they leave. Multiple roles per channel and multiple channels per role.

* **Say / Embed / Edit.** Mods can send or edit messages and embeds as the bot in a channel (e.g. for announcements the whole team can edit).

* **RemindMe.** Get a DM at a time you set, with your chosen message.

* **Raffles.** Create giveaways; users join by command or by reacting (🎰). Pick a random winner from the entries.

### Commands overview

The bot uses **slash commands** (`/`) as the main interface. Use `/help` in the bot to list commands by module. Optional prefix commands (e.g. `.` from config) are supported in DMs only and are less featured.

**General:** `help`, `about`, `ping`, `invite`, `avatar`, `roll`, `pick`, `joke`. **Channel:** `prune`. **Server settings:** mod-only mode, moderator roles, bot log channel, donghua in schedule.

## Support

**Official Discord Support Server:** https://discord.gg/BDT8Twv

Use `/invite` in the bot to get an invite link with recommended permissions (or invite the bot with `bot` and `applications.commands` scope).

This application is the next version of [ZeroTsu](https://github.com/apiks/ZeroTsu).

---

## Setup & Run

* **Requirements:** Go 1.26+, MongoDB. You need a Discord bot token (from the [Discord Developer Portal](https://discord.com/developers/applications)) and a running MongoDB instance (local or remote).

* **Environment:** Set `ZeroTsuToken` to your Discord bot token.

* **Config:** Copy `config.json.example` to `config.json`. The file must be in the **current working directory** when you run the bot.

  Set at least `mongo_uri`, `owner_id`, `prefixes` (used for optional DM prefix commands, e.g. `["."]`), and `user_agent` (required for AnimeSchedule and Reddit requests). Other options: `mongo_db_timeout`, `playing_msg`, `anime_schedule_api_key`, `top_gg_token`, `new_guild_log_channel_id`, `log_level`, `log_format`. See the example file for all keys. Anime features (subs, schedule, autopost) require `anime_schedule_api_key` in config (get it from [AnimeSchedule.net](https://animeschedule.net) or leave empty to disable).

* **Build:** `go build` (e.g. `go build -o zerotsu.exe`).

* **Run:** From the same directory as `config.json`, run the binary (e.g. `.\zerotsu.exe`). The bot uses Discord sharding; no extra configuration needed.
