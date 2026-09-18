/*
 * The Local server may replace or populate this object before app.js runs.
 * Supported fields:
 *   token:   per-launch bearer token
 *   apiBase: optional same-origin API prefix (defaults to "")
 */
globalThis.BELAY_LOCAL_CONFIG = globalThis.BELAY_LOCAL_CONFIG || {};
