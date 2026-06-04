package permissions

// MetadataNetworkHost carries the target host of a network operation (e.g. the
// host of a WebFetch URL) for approval display and per-host session grants.
// Only the host is exposed — never the full URL with its path/query.
const MetadataNetworkHost = "network_host"
