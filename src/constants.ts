import pluginJson from './plugin.json';

export const PLUGIN_BASE_URL = `/a/${pluginJson.id}`;
export const APP_PLUGIN_ID = pluginJson.id;
export const FORECAST_RESOURCE = `/api/plugins/${pluginJson.id}/resources/forecast`;
export const PLUGIN_HEALTH_URL = `/api/plugins/${pluginJson.id}/health`;

export enum ROUTES {
  Home = '',
}
