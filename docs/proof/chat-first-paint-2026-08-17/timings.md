## Headline: paired, interleaved, shipped bundle

Eight pairs. Each pair loads both bundles back to back in the same browser process, alternating which arm goes first, so machine load drift hits both arms equally. Bundles verified distinct by their compiled `APP_BUILD_HASH` (`perf-baseline` and `perf-after2`), each set on the build invocation that produced it.

| pair | before | after |
|---|---|---|
| 0 | 4305 ms | 2071 ms |
| 1 | 5519 ms | 3714 ms |
| 2 | 4433 ms | 3138 ms |
| 3 | 5134 ms | 2510 ms |
| 4 | 5724 ms | 2184 ms |
| 5 | 3230 ms | 1849 ms |
| 6 | 4475 ms | 2072 ms |
| 7 | 5636 ms | 3052 ms |
| **median** | **5134 ms** | **2510 ms** |
| median total blocking time | 1525 ms | 797 ms |

After wins 8 of 8 pairs. Median improvement 2624 ms.

This window was busy (system load average 20 to 35 on a 24 core machine), which inflates both arms. That is why the pairing matters, and it exposes a real property rather than only noise: a loaded client amplifies chain depth, because every extra serial step pays its own scheduling delay on top of its round trip.

# Sequential runs, same harness, calmer window

## Before, bundle built from main
composer visible, ms: 1872, 2551, 2886, 3115, 3281, 3301
median: 3115 ms

API waterfall for one run (start -> end, duration):

```
  783 ->   995  ( 212 ms)  /api/config
 1006 ->  1207  ( 201 ms)  /api/v1/auths/
 1210 ->  1424  ( 214 ms)  /api/config
 1445 ->  1738  ( 293 ms)  /api/v1/auths/update/timezone
 1499 ->  1737  ( 238 ms)  /api/v1/configs/banners
 1500 ->  1830  ( 330 ms)  /api/v1/tools/
 1501 ->  1855  ( 355 ms)  /api/v1/users/user/settings
 1514 ->  1846  ( 333 ms)  /api/v1/users/<user-id>/profile/image
 1545 ->  1851  ( 306 ms)  /api/version
 1859 ->  2677  ( 818 ms)  /api/models?
 2709 ->  3301  ( 591 ms)  /api/v1/terminals/
 2808 ->  3294  ( 485 ms)  /api/v1/users/<user-id>/profile/image
 3008 ->  3334  ( 327 ms)  /api/v1/models/model/profile/image?id=undefined&lang=en
 3021 ->  3312  ( 291 ms)  /api/v1/functions/
 3028 ->  3334  ( 306 ms)  /api/v1/models/model/profile/image?id=hive-auto&lang=en
 3316 ->  3525  ( 209 ms)  /api/v1/skills/
composer visible at 3115 ms
```

## After, first revision of this branch
composer visible, ms: 1783, 1920, 2026, 2091, 2192, 2298
median: 2091 ms

API waterfall for one run (start -> end, duration):

```
  754 ->  1015  ( 262 ms)  /api/v1/auths/
  756 ->  1554  ( 798 ms)  /api/models?
  757 ->  1018  ( 261 ms)  /api/config
 1057 ->  1377  ( 320 ms)  /api/v1/auths/update/timezone
 1124 ->  1366  ( 242 ms)  /api/v1/configs/banners
 1125 ->  1431  ( 306 ms)  /api/v1/tools/
 1125 ->  1410  ( 284 ms)  /api/v1/users/user/settings
 1135 ->  1427  ( 292 ms)  /api/v1/users/<user-id>/profile/image
 1556 ->  1753  ( 197 ms)  /api/version
 1562 ->  2104  ( 542 ms)  /api/v1/terminals/
 1640 ->  2181  ( 541 ms)  /api/v1/users/<user-id>/profile/image
 1881 ->  2211  ( 330 ms)  /api/v1/models/model/profile/image?id=undefined&lang=en
 1900 ->  2192  ( 292 ms)  /api/v1/functions/
 1914 ->  2208  ( 294 ms)  /api/v1/models/model/profile/image?id=hive-auto&lang=en
 2199 ->  2405  ( 206 ms)  /api/v1/skills/
composer visible at 2026 ms
```

## After, shipped bundle including the session guard
composer visible, ms: 3123, 3831
median: 3831 ms

API waterfall for one run (start -> end, duration):

```
 1680 ->  1928  ( 249 ms)  /api/v1/auths/
 1685 ->  2457  ( 772 ms)  /api/models?
 1688 ->  1943  ( 255 ms)  /api/config
 1992 ->  2323  ( 330 ms)  /api/v1/auths/update/timezone
 2072 ->  2339  ( 267 ms)  /api/v1/configs/banners
 2076 ->  2357  ( 281 ms)  /api/v1/tools/
 2084 ->  2367  ( 283 ms)  /api/v1/users/user/settings
 2102 ->  2422  ( 320 ms)  /api/v1/users/<user-id>/profile/image
 2465 ->  3128  ( 663 ms)  /api/v1/terminals/
 2599 ->  3193  ( 595 ms)  /api/v1/users/<user-id>/profile/image
 2888 ->  3232  ( 344 ms)  /api/v1/models/model/profile/image?id=undefined&lang=en
 2906 ->  3267  ( 361 ms)  /api/v1/functions/
 2918 ->  3294  ( 376 ms)  /api/v1/models/model/profile/image?id=hive-auto&lang=en
 2991 ->  3232  ( 241 ms)  /api/version
 3273 ->  3466  ( 193 ms)  /api/v1/skills/
composer visible at 3123 ms
```

