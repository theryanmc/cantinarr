package trakt

// DefaultClientID is Cantinarr's built-in Trakt API client ID, registered as
// the project's own "Cantinarr" application.
//
// It is DELIBERATELY PUBLIC — the same model as the built-in TMDB token one
// package over. Everything Cantinarr asks Trakt for (trending, popular,
// lists, calendar, id lookups) is a public GET that authenticates with
// nothing but this ID; it unlocks no account on any Cantinarr instance, and
// the server caches every feed for minutes at a time, so the shared request
// budget goes a very long way. Do not move it into the encrypted credential
// store, and do not treat its appearance in the repo, images, or request
// headers as a leak. Rotation happens through a normal release.
//
// An admin-supplied client ID (Settings > Discover) always takes precedence
// over this one — and that value IS a personal credential, handled as a
// secret like every other stored key. An empty value here (a build without a
// bundled application) simply leaves Trakt unconfigured until an admin
// stores an ID.
const DefaultClientID = "omDqaPz66jp0CfFYrkOquUcnNGfgDn2KiyzHCzOfaL8"
