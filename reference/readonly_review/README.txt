OC TEST read-only runtime evidence
Implementation: 89fb095a309e1a14ff69fa0b6d96fdad48686a39
Preceding raw-audit implementation: 2290dc9
Host: local Ubuntu 20.04 x86_64 development environment

This is OFFLINE evidence. No real broker API object, Redis connection, login,
settlement confirmation or trade was executed by these test scenarios.
Vendor shared libraries were loaded for version/hash checks only.
The runtime test injects a fake API object exercising the real driver SPI and
an in-memory IO implementation exercising real local journal/outbox/ACK state.
Stream keys, IDs and SDK responses in the JSON are synthetic. Do not publish
these messages to Redis. Authentication log lines refer to simulated callbacks.

readonly_runtime.v1.json is the final pretty-printed JSON from runtime.stdout.txt,
extracted without rewriting any message body string. It contains eight scenarios,
including original input body strings, output body strings and heartbeat objects.
The four-query case has one nonempty asset result and three complete empty results.
It does not demonstrate nonempty real orders/trades or live event association.
runtime.stderr.txt preserves original glog diagnostics, including its initialization
warning. Log dates reflect collection time, not the planned live assessment window.

ctest.txt: 32 ordinary CTest entries, all passed.
asan-ubsan-nopie.txt: two raw-audit/runtime entries, all passed with
ASAN_OPTIONS=detect_leaks=1 UBSAN_OPTIONS=halt_on_error=1.
Non-PIE build flags: -fsanitize=address,undefined -fno-omit-frame-pointer -fno-pie
Link flags: -fsanitize=address,undefined -no-pie
Run the two builds sequentially: they deliberately use the same synthetic account
and therefore conflict on the same OS-user account lock when executed concurrently.
This does not resolve the previously tracked PIE sanitizer issue.

Reproduce from the project build directory:
  ctest --output-on-failure
  ./ctp_readonly_runtime_test > runtime.stdout.txt 2> runtime.stderr.txt
  awk '/^\{$/ {copy=1} copy' runtime.stdout.txt > readonly_runtime.v1.json
From build/readonly-sanitized-nopie:
  ASAN_OPTIONS=detect_leaks=1 UBSAN_OPTIONS=halt_on_error=1 ctest -R '^ctp_readonly_(audit|runtime)_test$' --output-on-failure

No live Redis/PG, CentOS7, recovery across real broker sessions, or trading
acceptance is claimed. SHA256SUMS covers these artifacts and the handoff document.
