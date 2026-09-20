import { plugin } from './module';

// The tab is Grafana's only entry point to the schedules table: configPages drives the
// tab bar, and configPages[0] is the tab /plugins/<id> lands on.
test('registers Configuration then Retrain schedules as app config pages', () => {
  expect(plugin.configPages?.map((page) => [page.id, page.title])).toEqual([
    ['configuration', 'Configuration'],
    ['schedules', 'Retrain schedules'],
  ]);
});
