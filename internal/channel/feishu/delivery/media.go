package delivery

// TODO(agent-engine): expose an authorized Runtime-owned output artifact (for
// example a bounded local file/blob with MIME type, size, and digest) before
// producing Feishu image/file deliveries. Engine resource_link output is an
// external HTTP(S) URL; the channel must not fetch it as media because that
// would add an SSRF/authorization fallback outside the Runtime boundary.
