import type { Configuration } from 'webpack';
import CopyWebpackPlugin from 'copy-webpack-plugin';
import grafanaConfig, { type Env } from './.config/webpack/webpack.config';

const config = async (env: Env): Promise<Configuration> => {
  const base = await grafanaConfig(env);
  if (base.entry && typeof base.entry === 'object' && !Array.isArray(base.entry)) {
    const fixed: Record<string, unknown> = {};
    for (const [name, value] of Object.entries(base.entry)) {
      fixed[name.replace(/\\/g, '/').replace(/^\/+/, '')] = value;
    }
    base.entry = fixed as Configuration['entry'];
  }
  base.plugins = base.plugins ?? [];
  base.plugins.push(
    new CopyWebpackPlugin({
      patterns: [{ from: 'forecast-panel/img', to: 'forecast-panel/img', noErrorOnMissing: true }],
    })
  );
  return base;
};

export default config;
