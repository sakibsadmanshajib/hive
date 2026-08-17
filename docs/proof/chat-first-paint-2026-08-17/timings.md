## Before (bundle built from main)
composer visible, ms: 1872, 2551, 2886, 3115, 3281, 3301
median: 3115 ms

API waterfall, run 4 of 6 (start -> end, duration):

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

## After (bundle built from this branch)
composer visible, ms: 1783, 1920, 2026, 2091, 2192, 2298
median: 2091 ms

API waterfall, run 4 of 6 (start -> end, duration):

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
