# Subscriptions match a show by its route first, then by its display title

A subscription stores the show's AnimeSchedule route alongside its display title. The new-episodes notifier and the cleanup match a subscription to timetable entries by route first and fall back to the title (compared with the shared match key) only when the subscription has no route. A subscription that matches by title picks up the entry's route then. This reverses the earlier rule that subscriptions are keyed by the display title alone. We changed it because members can now subscribe to upcoming shows, and AnimeSchedule often renames a show between announcement and premiere. A title-only subscription would then never match and would be deleted by the cleanup. We kept the title fallback rather than migrating every stored subscription, because existing subscriptions have no route and the bot cannot look one up reliably from a title.

## Consequences

Both fields must be kept in step: the title is still what members see and what `/subscription remove` and the Remove picker resolve, and the route is what matching trusts. A route that AnimeSchedule itself changes breaks matching the same way a rename used to, but that is rare.

Show names are still compared only through the shared match key, which the source checks enforce.
