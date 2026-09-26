# Changelog

## [0.7.0](https://github.com/moudlajs/waiverwatch/compare/v0.6.1...v0.7.0) (2026-09-26)


### Features

* **auth:** sign in with your Sleeper username ([#64](https://github.com/moudlajs/waiverwatch/issues/64)) ([1017f0c](https://github.com/moudlajs/waiverwatch/commit/1017f0c61799ba10fee416445c77840c4b6dd845))


### Documentation

* multi-user design ([#62](https://github.com/moudlajs/waiverwatch/issues/62)) ([e03ed8f](https://github.com/moudlajs/waiverwatch/commit/e03ed8fdab828899177c3677bd7a0f3c1c28f1d2))

## [0.6.1](https://github.com/moudlajs/waiverwatch/compare/v0.6.0...v0.6.1) (2026-09-26)


### Bug Fixes

* **deploy:** enable the Cloud Resource Manager API for the kill switch ([#60](https://github.com/moudlajs/waiverwatch/issues/60)) ([45a4a73](https://github.com/moudlajs/waiverwatch/commit/45a4a7387989f6a81cd2dffccbe86e46943377ff))

## [0.6.0](https://github.com/moudlajs/waiverwatch/compare/v0.5.1...v0.6.0) (2026-09-26)


### Features

* **deploy:** billing kill switch when the budget is exceeded ([#58](https://github.com/moudlajs/waiverwatch/issues/58)) ([feebb41](https://github.com/moudlajs/waiverwatch/commit/feebb41eda81bce65490ee8a5631621df979b2ee))

## [0.5.1](https://github.com/moudlajs/waiverwatch/compare/v0.5.0...v0.5.1) (2026-09-26)


### Bug Fixes

* **auth:** let the browser follow the redirect back to Claude ([#48](https://github.com/moudlajs/waiverwatch/issues/48)) ([c044bbd](https://github.com/moudlajs/waiverwatch/commit/c044bbd4507f70efdf62bc90e462f8a65f979a93))

## [0.5.0](https://github.com/moudlajs/waiverwatch/compare/v0.4.1...v0.5.0) (2026-09-25)


### Features

* **mcp:** draft_results tool: who I drafted, and where ([#45](https://github.com/moudlajs/waiverwatch/issues/45)) ([807a452](https://github.com/moudlajs/waiverwatch/commit/807a452212fa249af942af0aa30145b2545557ba))
* **mcp:** OAuth sign-in for the hosted connector ([#43](https://github.com/moudlajs/waiverwatch/issues/43)) ([d021889](https://github.com/moudlajs/waiverwatch/commit/d0218895208c5f72d9ed19e34b1d32d4aafec5f7))

## [0.4.1](https://github.com/moudlajs/waiverwatch/compare/v0.4.0...v0.4.1) (2026-09-25)


### Bug Fixes

* **mcp:** serve the health check at /health, not /healthz ([#39](https://github.com/moudlajs/waiverwatch/issues/39)) ([3937090](https://github.com/moudlajs/waiverwatch/commit/39370903d73720d3501f82d57e07d11d95a3c5bf))

## [0.4.0](https://github.com/moudlajs/waiverwatch/compare/v0.3.0...v0.4.0) (2026-09-25)


### Features

* **mcp:** compare_rosters tool for my team vs an opponent ([#32](https://github.com/moudlajs/waiverwatch/issues/32)) ([d2172c3](https://github.com/moudlajs/waiverwatch/commit/d2172c31fd542312da5dd8ffffe000a59be5d6d0))
* **mcp:** Streamable HTTP transport for hosting ([#35](https://github.com/moudlajs/waiverwatch/issues/35)) ([3682f5e](https://github.com/moudlajs/waiverwatch/commit/3682f5e5240ed05dcb27b4594ffefd8c2cbc4d44))

## [0.3.0](https://github.com/moudlajs/waiverwatch/compare/v0.2.0...v0.3.0) (2026-09-25)


### Features

* **mcp:** get_roster tool with names and injury status ([#29](https://github.com/moudlajs/waiverwatch/issues/29)) ([d2bd967](https://github.com/moudlajs/waiverwatch/commit/d2bd967fbdd38412bfa3f00316680037800aa7b6))

## [0.2.0](https://github.com/moudlajs/waiverwatch/compare/v0.1.0...v0.2.0) (2026-09-25)


### Features

* **mcp:** get_matchups tool for this week across all leagues ([#25](https://github.com/moudlajs/waiverwatch/issues/25)) ([e2adac6](https://github.com/moudlajs/waiverwatch/commit/e2adac6ac4e615105ac4ec010995f73a92bd8e17))
* **mcp:** trending_players tool with availability in my leagues ([#27](https://github.com/moudlajs/waiverwatch/issues/27)) ([2961b5e](https://github.com/moudlajs/waiverwatch/commit/2961b5e9c4aef4899f023f02c271cd512389e677))
* **mcp:** waiver_targets tool for available players by position ([#28](https://github.com/moudlajs/waiverwatch/issues/28)) ([af2afd9](https://github.com/moudlajs/waiverwatch/commit/af2afd9d2e7833aa90243faa4df6dd13e85809dc))

## 0.1.0 (2026-09-25)


### Features

* **mcp:** stdio MCP server with a list_leagues tool ([#23](https://github.com/moudlajs/waiverwatch/issues/23)) ([4ac5b25](https://github.com/moudlajs/waiverwatch/commit/4ac5b25abdd2448a497d6a990c1cc5643a5f7900))
* **sleeper:** player dictionary cache for ID-to-name lookups ([#22](https://github.com/moudlajs/waiverwatch/issues/22)) ([135e6c1](https://github.com/moudlajs/waiverwatch/commit/135e6c1662dc1e30870b2177d42ef8a13e7d2566))


### Refactoring

* **sleeper:** move the API client into internal/sleeper ([#19](https://github.com/moudlajs/waiverwatch/issues/19)) ([e3cafcf](https://github.com/moudlajs/waiverwatch/commit/e3cafcf5a756b9fdf6d823fbc760fbd909e42e6e))
