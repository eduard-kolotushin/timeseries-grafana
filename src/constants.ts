import pluginJson from './plugin.json';

export const APP_PLUGIN_ID = pluginJson.id;
export const FORECAST_RESOURCE = `/api/plugins/${pluginJson.id}/resources/forecast`;
