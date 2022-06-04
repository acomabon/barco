# File formats

All file encoding is big-endian.

## Log segment file

The log segment is composed of compressed chunks.

### Chunk

```
+---------+----------+---------------+---------------+----------+
| flags   | length   |  start offset | record length | head crc |
| (uint8) | (uint32) |     (int64)   |    (uint32)   | (int32)  |
+---------+----------+---------------+---------------+----------+
| compressed payload (zstd crc)                                 |
| +-------------------------------------------------+           |
| | record                                          |           |
| | +--------------------------+------------------+ |           |
| | | timestamp micros (int64) | length (uint32)  | |           |
| | +--------------------------+------------------+ |           |
| | |             body (bytes)                    | |           |
| | +---------------------------------------------+ |           |
| +-------------------------------------------------+           |
|                                                               |
| +-------------------------------------------------+           |
| | record...                                       |           |
| +-------------------------------------------------+           |
|                                                               |
| .                                                             |
| .                                                             |
| .                                                             |
|                                                               |
+---------------------------------------------------------------+
| alignment (0x80) - not  included in header length             |
+---------------------------------------------------------------+
```

## Index file

The index file is composed by a series of message offset, file offset and checksum tuples.

```
+-------------------------------------------------------------------+
| items                                                             |
| +---------------------------------------------------------------+ |
| | item                                                          | |
| | +-----------------+---------------------+-------------------+ | |
| | | offset (int64)  | file offset (int64) | checksum (uint32) | | |
| | +-----------------+---------------------+-------------------+ | |
| +---------------------------------------------------------------+ |
|                                                                   |
| +---------------------------------------------------------------+ |
| | item...                                                       | |
| +---------------------------------------------------------------+ |
|                                                                   |
| .                                                                 |
| .                                                                 |
| .                                                                 |
|                                                                   |
+-------------------------------------------------------------------+
```