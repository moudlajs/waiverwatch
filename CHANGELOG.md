# Changelog

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
