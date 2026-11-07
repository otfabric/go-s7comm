# Recorded S7 traffic of real CPUs

Network captures between real Siemens S7-300 / S7-400 CPUs and several
clients (Siemens engineering and HMI software, Snap7, libnodave). go-s7comm's
tests decode every recorded PDU, replay the CPUs' answers to its client, and
compare its server's answers with the CPUs'. This is evidence from real
devices, recorded by others; it is not a test against a live PLC and not a
conformance claim.

## Sources and terms

| Directory | Source | Revision | Terms |
|---|---|---|---|
| `gymgit/` | [gymgit/s7-pcaps](https://github.com/gymgit/s7-pcaps) by Gyorgy Miru, 2016 | commit `88f35a601de45fa29b0b36048a30ae4a1e925320` | No licence file. Its README: the pcaps were "created for public use"; addresses were randomised with tcprewrite. [lexab/s7Comm-pcaps](https://github.com/lexab/s7Comm-pcaps) is the same repository (same commit, identical files) and is not copied twice. |
| `wireshark/` | [Wireshark wiki, S7Comm](https://wiki.wireshark.org/S7Comm) sample captures (also listed on [SampleCaptures](https://wiki.wireshark.org/SampleCaptures)) | downloaded 2026-11-07 | No licence statement; published as sample captures for the S7comm dissector. |

Left out: `tia_s300_updateFirmware.pcapng` and `tia_s300_updateFirmware_2.pcapng`
(14 MB each, byte-identical to each other, a firmware image rather than S7
communication worth testing) and the gymgit `README.md`. Nothing else was
changed: the files are byte-identical to their sources, checked by
[SHA256SUMS](SHA256SUMS) (`shasum -a 256 -c SHA256SUMS` in this directory).

## Names

`<client>_<plc-family>_<operation>.<ext>`, lower case, words joined by
hyphens; the extension keeps the original format (pcap or pcapng).

| Client | Software |
|---|---|
| `snap7` | Snap7 client demo (C++), [Snap7](https://snap7.sourceforge.net/) |
| `step7-v5.5` | SIMATIC STEP 7 V5.5 (Manager) |
| `tia-v13` | TIA Portal V13 |
| `wincc-advanced` | WinCC Advanced runtime |
| `libnodave` | libnodave demo programs |
| `unknown` | not stated by the source; a PG connection (TSAP 0x0100), most likely STEP 7 |

## Devices

Read from the CPUs' own answers (SZL 0x0011 and 0x001C) where the traffic
holds them.

| Device | Order number | Firmware | Seen in |
|---|---|---|---|
| CPU 315-2 PN/DP ("PLC_1", station "S7300/ET200M station_1", serial S C-B1U393142011) | 6ES7 315-2EH14-0AB0 | V3.2.7 | gymgit `snap7_*`, `tia-v13_*` (same serial); the other gymgit S7-300 captures most likely the same CPU (not identified in their traffic) |
| IM151-8 PN/DP CPU (ET 200S, S7-300 class; serial S C-C6TW74882012) | 6ES7 151-8AB01-0AB0 | V3.2.6 | wireshark `unknown_*` |
| S7-300, model not in the traffic | | | wireshark `libnodave_*` |
| S7-400, model not in the traffic | | | gymgit `wincc-advanced_s7-400_production` |

## Files

PDUs: S7 PDUs in the file, equal to what Wireshark 4.6.9 decodes. Gaps: TCP
segments the source left out of the capture (the gymgit files were filtered
after recording); none cuts a PDU.

| File | Original name | PDUs | Gaps | What it holds |
|---|---|---:|---:|---|
| `gymgit/snap7_s7-300_client-demo.pcapng` | `snap7_s300_everything.pcapng` | 64 | 0 | Snap7 client demo: setup (PDU 480 → 240), SZL 0x0011/0x001C (two packets)/0x0131/0x0424, block counts, SDB 0 upload (216 bytes), block info DB 0 (refused 0x0A), multi-variable read and write of M, I, Q, timer and counter (CPU answers the timer and counter writes with item code 0x03), PLC stop, copy RAM to ROM |
| `gymgit/snap7_s7-300_client-demo.log` | `snap7_s300_events.txt` | | | Snap7's console output of the same session: the values the CPU reported |
| `gymgit/snap7_s7-300_read-var.pcapng` | `snap7_s300_readVar.pcapng` | 2 | 0 | Read DB1 (CPU answers 0x0A: the DB does not exist) |
| `gymgit/snap7_s7-300_setup-communication.pcapng` | `snap7_s300_setupCommunication.pcapng` | 2 | 0 | Setup communication, PDU 1920 requested, 240 granted |
| `gymgit/snap7_s7-300_plc-stop.pcapng` | `snap7_s300_stop.pcapng` | 2 | 0 | PLC stop |
| `gymgit/step7-v5.5_s7-300_auth-password.pcapng` | `step7_s300_AuthPassword.pcapng` | 6 | 2 | Password authentication, SZL 0x0132/0x0424 |
| `gymgit/step7-v5.5_s7-300_copy-ram-to-rom.pcapng` | `step7_s300_copyRamToRom.pcapng` | 2 | 0 | Copy RAM to ROM |
| `gymgit/step7-v5.5_s7-300_download-blocks.pcapng` | `step7_s300_download.pcapng` | 96 | 59 | Block download |
| `gymgit/step7-v5.5_s7-300_plc-stop.pcapng` | `step7_s300_stop.pcapng` | 4 | 0 | PLC stop, with the CPU's mode-transition message |
| `gymgit/step7-v5.5_s7-300_read-diag-data.pcapng` | `step7_s300_readDiagData.pcapng` | 410 | 0 | Diagnostic data, cyclic |
| `gymgit/step7-v5.5_s7-300_read-var-table.pcapng` | `step7_s300_readVarTab.pcapng` | 331 | 0 | Variable table, read |
| `gymgit/step7-v5.5_s7-300_read-write-var-table.pcapng` | `step7_s300_rwVarTab.pcapng` | 16 | 0 | Variable table, read and write |
| `gymgit/tia-v13_s7-300_download-hw-config.pcapng` | `tia_s300_downloadHwConfig.pcapng` | 154 | 90 | Hardware configuration download, six connections |
| `gymgit/tia-v13_s7-300_download-ob1.pcapng` | `tia_s300_downloadOb1.pcapng` | 106 | 74 | OB 1 download |
| `gymgit/tia-v13_s7-300_flash-led.pcapng` | `tia_s300_flashLed.pcapng` | 4 | 1 | Flash LED |
| `gymgit/tia-v13_s7-300_go-online.pcapng` | `tia_s300_goOnline.pcapng` | 448 | 223 | Go online: SZL lists, block counts and lists of every type (SFC in two packets), block info, protection, diagnostic buffer |
| `gymgit/wincc-advanced_s7-300_alarm-read-1.pcapng` | `wincc_s300_setup-alarm-read.pcapng` | 128 | 41 | WinCC: alarm setup, cyclic reads of DB, M, timers, counters |
| `gymgit/wincc-advanced_s7-300_alarm-read-2.pcapng` | `wincc_s300_setup-alarm-read_2.pcapng` | 486 | 235 | Same, longer |
| `gymgit/wincc-advanced_s7-300_alarm-read-write.pcapng` | `wincc_s300_setup-alarm-read-write.pcapng` | 164 | 79 | Same, with writes of M bits |
| `gymgit/wincc-advanced_s7-400_production.pcapng` | `wincc_s400_production.pcapng` | 1658 | 423 | S7-400 in production: DBREAD (syntax 0xB0) reads, DB writes, alarms, CPU state |
| `wireshark/unknown_s7-300_download-db1.pcap` | `s7comm_downloading_block_db1.pcap` | 48 | 0 | Download DB 1 |
| `wireshark/unknown_s7-300_list-blocks.pcap` | `s7comm_program_blocklist_onlineview.pcap` | 70 | 0 | Block counts and lists |
| `wireshark/unknown_s7-300_read-plc-status.pcap` | `s7comm_reading_plc_status.pcap` | 144 | 0 | SZL lists, CPU state, diagnostic buffer |
| `wireshark/unknown_s7-300_read-set-clock.pcap` | `s7comm_reading_setting_plc_time.pcap` | 26 | 0 | Read and set the clock |
| `wireshark/libnodave_s7-300_read-write-var.pcap` | `s7comm_varservice_libnodavedemo.pcap` | 16 | 0 | libnodave demo: reads and writes of several areas |
| `wireshark/libnodave_s7-300_benchmark.pcap` | `s7comm_varservice_libnodavedemo_bench.pcap` | 10006 | 0 | libnodave benchmark: 3000 reads, 2002 writes |

25 captures, 14,393 S7 PDUs.

## How the tests use them

| Test | Checks |
|---|---|
| `internal/capture` `TestCapturesMatchWireshark` | The reader (pcap and pcapng, Ethernet, IPv4, TCP reassembly, TPKT, COTP) finds per file the PDUs Wireshark 4.6.9 decodes |
| `wire` `TestCaptures*`, `TestDBReadFixturesFromWinCC` | Every PDU decodes; WinCC's 84 DBREAD requests to the S7-400 parse, three of them as byte-exact fixtures; Read Var, Write Var and Setup Communication decode both ways, and every successful item has the requested length; every user data request of the five clients has the parameter layout `wire` encodes; the encoders produce requests the real clients sent, byte for byte; the CPU's item code 0x03 for a refused write |
| `client` `TestCaptureReplay*` | The client runs against the recorded answers; a request no real client sent finds no answer and fails the test. Identify, CPU state, protection, diagnostic buffer, block counts, lists and info, and SDB 0 upload give the values in Snap7's log and Wireshark's decode |
| `server` `TestCaptureReplayMatchesRealCPUs` | Every recorded Setup Communication, Read Var and Write Var job, sent to `server` holding the same memory, gets the CPU's answer byte for byte: 26 setups, 3,411 reads (timers, counters and DBREAD included), 2,081 writes |

Adding a capture: copy it here under the naming scheme, add its line to
SHA256SUMS and to the tables above, and its expected counts to
`internal/capture/capture_test.go` (cross-check with
`tshark -r <file> -Y 's7comm && !tcp.analysis.retransmission && !tcp.analysis.out_of_order' -T fields -e s7comm.header.rosctr`).
