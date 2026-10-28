// Expo reads app.json, then this file. AM_UPDATES_URL points a test build's
// over-the-air updates at a local server, for example
// AM_UPDATES_URL=http://localhost:8765/api/ota/manifest (iOS Simulator).
module.exports = ({ config }) =>
  process.env.AM_UPDATES_URL
    ? { ...config, updates: { ...config.updates, url: process.env.AM_UPDATES_URL } }
    : config;
