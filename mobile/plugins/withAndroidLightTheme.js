// The app has one, light design (app.json → userInterfaceStyle "light"), but
// Expo's Android template themes the activity with a DayNight parent. With
// the phone in dark mode, AppCompat then draws React Native's Alert dialogs
// (and the window background) dark over the light app. A light parent keeps
// them light. AppCompat also draws the dialogs' buttons, the text cursor and
// the selection handles in colorAccent, which the template leaves at
// AppCompat's teal: point it at colorPrimary (app.json → primaryColor, the
// app's sage).
const { AndroidConfig, withAndroidStyles } = require("expo/config-plugins");

module.exports = function withAndroidLightTheme(config) {
  return withAndroidStyles(config, (config) => {
    const theme = config.modResults.resources.style?.find((s) => s.$.name === "AppTheme");
    if (theme) theme.$.parent = "Theme.AppCompat.Light.NoActionBar";
    config.modResults = AndroidConfig.Styles.assignStylesValue(config.modResults, {
      add: true,
      parent: AndroidConfig.Styles.getAppThemeGroup(),
      name: "colorAccent",
      value: "@color/colorPrimary",
    });
    return config;
  });
};
