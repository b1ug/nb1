# Daemon shutdown diagnostics (experimental)

The candidate emits `daemon_shutdown_trigger` and `daemon_shutdown_result` using `nb1-daemon-lifecycle/experimental-v1`. These are bounded log records, not new HTTP/MCP APIs. They add no USB operations and do not change retries, ownership, or cleanup order.

A connection event's `last_error` is device state. It is not proof of why the daemon stopped. The trigger records the branch observed by the existing shutdown select: `parent_context_done`, `engine_stopped`, `http_server_returned`, or `startup_log_failed`. Multiple branches can be ready simultaneously. The context field describes the sampled parent state; it does not identify an OS signal or crash.

The result records fixed error classes for HTTP drain and Engine.Close, whether Engine.Done was observed, elapsed milliseconds, and the existing return outcome. Classes are `none`, `context_canceled`, `deadline_exceeded`, `http_server_closed`, or `other`; arbitrary errors are not inspected or copied into these new records. `other` deliberately loses native detail. Existing connection/error behavior is unchanged.

Exactly two typed records use a capacity-two queue and the existing single log writer. Sending does not wait for the output sink, and hardware shutdown does not join the logger. A blocked/failed sink or process exit may lose either record. Missing logs do not mean a clean shutdown. Error-free logging does not provide a native I/O deadline or force-release an in-flight device handle.

Software validation uses fake backends and private loopback servers, including a blocked HTTP body, failed/blocked writers, and native cleanup exceeding its waiting deadline while retaining ownership. The fixed candidate has no physical-device or long-running reliability qualification yet. It helps investigate future exits; it does not explain the earlier permission-related exit.
