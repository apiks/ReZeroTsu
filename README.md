# ZeroTsu is a Discord BOT with anime and reddit functionalities

<p align="center">
	<img src="https://images-wixmp-ed30a86b8c4ca887773594c2.wixmp.com/f/6e4868e2-f52b-4c7d-a984-d5027576b221/dch684c-818cbf96-b76b-4e75-8445-75d1497195b7.png?token=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1cm46YXBwOjdlMGQxODg5ODIyNjQzNzNhNWYwZDQxNWVhMGQyNmUwIiwiaXNzIjoidXJuOmFwcDo3ZTBkMTg4OTgyMjY0MzczYTVmMGQ0MTVlYTBkMjZlMCIsIm9iaiI6W1t7InBhdGgiOiJcL2ZcLzZlNDg2OGUyLWY1MmItNGM3ZC1hOTg0LWQ1MDI3NTc2YjIyMVwvZGNoNjg0Yy04MThjYmY5Ni1iNzZiLTRlNzUtODQ0NS03NWQxNDk3MTk1YjcucG5nIn1dXSwiYXVkIjpbInVybjpzZXJ2aWNlOmZpbGUuZG93bmxvYWQiXX0.w_Pmn6zmDv4NcB9h-lPko3-7qnvGmLqVD7862q59XR8" alt="Zero Two" width="300" height="300">
</p>

## Features

* **New Anime Episodes.** Optional DM notifications when new episodes release (always the sub, else raw); optional channel autopost with role ping that follows the server's preferred track: Sub posts sub (else raw) episodes, Dub posts dubbed episodes plus the sub (else raw) episodes of shows without a dub that week, Raw posts raw episodes. Source: [AnimeSchedule.net](https://animeschedule.net).

* **Daily Anime Schedule.** View or autopost the daily schedule; `/schedule` has a day picker for the three loaded weeks plus Previous and Next buttons, and an optional `track` option (Sub, Dub or Raw) that sets which track a day shows (omitted, it follows the server's preferred track, which moderators cycle Sub, Dub, Raw with the Preferred track button on `/settings`; Sub by default, and always Sub in DMs). The daily-schedule autopost follows the same preference. Source: [AnimeSchedule.net](https://animeschedule.net).

* **Preferred name.** A server picks which of a show's names it sees: Title (AnimeSchedule's own title, the default), Romaji, English or Native. Moderators cycle it with the Preferred name button on `/settings`. `/schedule` in the server, the daily-schedule autopost and the new-episodes autopost (its embed title and a movie's "is out" line) call each show by that name, or by its title when the show doesn't have it. A member picks their own with `/subscription name` (the same four, Title by default, never taken from a server). It names shows in their episode DMs (the embed title and a movie's "is out" line), in every `/subscription` reply even inside a server that prefers another name (the `add` confirmation and next-episode line, `list` lines, the Remove picker, the `remove` reply, its suggestions and a name typed into it, which match the shown name or the title; a show no longer in the loaded weeks or the upcoming list keeps its stored title; `add` suggestions read "Shown name (Title)" when the two differ, and "Title (English)" under Title, as before), and in `/schedule` wherever there is no server setting: in DMs, and from a user install in a server the bot hasn't joined, including each Previous, Next or day press (another member's press gets a private copy in that member's own preferred name). In a server the bot has joined, the server's preferred name wins. Subscriptions and what was already posted or DMed are unaffected by a change.

* **Customizable Reddit RSS autopost.** Post threads by subreddit, sort (hot / rising / new), optional author or title filters, and optional auto-pin of the latest post. Reddit feeds retire on 13 November 2026, when Reddit shuts down its RSS feeds.

* **Reaction roles.** Give or remove roles using message reactions.

* **Voice channel roles.** Automatically assign roles when users join a voice channel and remove them when they leave. Multiple roles per channel and multiple channels per role.

* **Say and Edit message.** Mods post a message or embed as the bot with the `/say` form, and fix one it posted with the **Edit message** right-click (e.g. for announcements the whole team can edit).

* **RemindMe.** Get a DM at a time you set, with your chosen message.

* **Raffles.** Create giveaways; users join by command or by reacting (🎰). Pick a random winner from the entries.

### Commands overview

The bot uses **slash commands** (`/`). Use `/help` in the bot to browse commands by category. The only prefix commands are the owner's: `servers`, `shards`, `uptime` and `messagelogs`, sent in a DM to the bot. A DM that starts with a configured prefix and names none of them gets a one-line hint that slash commands replaced prefix commands.

**Everyone** (also usable under a user install, in any server, DM or group DM): `/schedule`, `/subscription` (`add`, `remove`, `list`, `clear`, `name`; `name` sets your preferred name, Title, Romaji, English or Native, or states it when run without a choice, works with no subscriptions, and survives `clear`; every `/subscription` reply calls shows by it; `add` also follows upcoming shows (it says when they premiere, or that the date isn't announced yet), says when the next episode airs and reminds you to keep DMs open, labels each suggestion with its media type (TV Short, ONA, OVA, Special or Movie) and "Donghua" for Chinese releases (as in "Movie · Donghua"; a Chinese ONA reads "ONA · Donghua" and a Chinese TV series reads just "Donghua"; a Chinese movie also gets movie wording, "is out, subbed!" in alerts and "It releases ..." in replies, like a Japanese one), leaves out donghua in a server that hides it (a typed donghua name there gets a pointer to run `add` in a DM, where donghua is always offered, or, for one you already follow, a note to run `add` in a DM to change its tracks; shows you already follow are untouched), and takes an optional `track` option of Sub (the default; a raw alert in a week the show has no sub), Dub, Raw, Sub and Dub, or Every track, which sets which airings DM you (run `add` again on a show you follow with a different `track` to change its tracks); `list` shows each show's next episode or when it last aired; a subscription that is not plain Sub names its tracks in both, labels the next episode "(sub)", "(dub)" or "(raw)" when it has several, and says "No dub is listed yet" (likewise raw) while a chosen track has no airing or announcement), `/reminder` (`set`, `list`, `remove`), `/roll`, `/pick`, `/joke`, `/avatar`, `/help`, `/about`, `/invite`, `/ping`, and the right-click user command **Show avatar**.

**Server settings:** `/settings` (bot log, new-episodes and daily-schedule autoposts, ping role, donghua in the schedule and new subscriptions, preferred track, preferred name, ping message). Mod commands default to Manage Server; server admins grant them to other roles, members or channels in Server Settings, Integrations.

**Voice roles:** `/voicerole add`, `/voicerole remove`, `/voicerole list`.

**Reddit feeds (retiring on 13 November 2026):** `/reddit add`, `/reddit remove`, `/reddit list`. The `post-type` option of `/reddit add` is now `sort` (Hot, Rising or New). `/reddit remove` takes one `feed` option: pick a feed from the suggestions (each shows its subreddit, sort, channel, author and title filter), or type a subreddit that has exactly one feed. <!-- archcheck:allow -->

**Reaction roles:** right-click a message for **Add reaction role** or **Remove reaction roles**, and `/reactrole list` to see and remove them.

**Raffles:** `/raffle create`, `/raffle draw`, `/raffle remove`, `/raffle list`, `/raffle post`.

**Posting and channels:** `/say` (opens a form for text or an embed), the right-click message command **Edit message**, and `/prune`.

## Support

**Official Discord Support Server:** https://discord.gg/BDT8Twv

Use `/invite` or `/about` in the bot to get an invite link with the permissions it uses (or invite the bot with `bot` and `applications.commands` scope).

This application is the next version of [ZeroTsu](https://github.com/apiks/ZeroTsu).

---

## Setup & Run

* **Requirements:** Go 1.26+, MongoDB. You need a Discord bot token (from the [Discord Developer Portal](https://discord.com/developers/applications)) and a running MongoDB instance (local or remote).

* **Environment:** Set `ZeroTsuToken` to your Discord bot token.

* **Config:** Copy `config.json.example` to `config.json`. The file must be in the **current working directory** when you run the bot.
  - **Required:** `mongo_uri`, `owner_id`, `prefixes` (e.g. `["."]`; the prefixes apply to the owner's DM commands and the retirement hint), `user_agent` (for AnimeSchedule and Reddit requests).
  - **Optional:** `mongo_db_timeout`, `playing_msg`, `anime_schedule_api_key`, `top_gg_token`, `new_guild_log_channel_id`, `log_level`, `log_format`. See the example file for all keys.
  - Anime features (subs, schedule, autopost) need `anime_schedule_api_key` — get it from [AnimeSchedule.net](https://animeschedule.net) or leave empty to disable.

* **Build:** `go build` (e.g. `go build -o zerotsu.exe`).

* **Run:** From the same directory as `config.json`, run the binary (e.g. `.\zerotsu.exe`). The bot uses Discord sharding; no extra configuration needed. Run exactly one bot instance against a given database.
