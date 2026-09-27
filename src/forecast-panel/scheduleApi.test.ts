import { deleteSchedule, deleteScheduleModel, listSchedules, postScheduleDefault, ScheduleRow } from './scheduleApi';

const mockGet = jest.fn();
const mockDelete = jest.fn();
const mockPost = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ get: mockGet, delete: mockDelete, post: mockPost }),
}));

const RESOURCE = '/api/plugins/eduardkolotushin-forecast-app/resources/schedules';

const row: ScheduleRow = {
  scope: 'panel',
  key: 'a'.repeat(64),
  cron: '0 3 * * *',
  timezone: 'UTC',
  enabled: true,
};

describe('scheduleApi', () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockDelete.mockReset();
    mockPost.mockReset();
  });

  it('lists the org schedules', async () => {
    mockGet.mockResolvedValue([row]);
    await expect(listSchedules()).resolves.toEqual([row]);
    // The path is the module's contribution: asserting only the resolved value would pass
    // for an implementation that never issued the request.
    expect(mockGet).toHaveBeenCalledWith(RESOURCE);
  });

  it('deletes by scope and key query params', async () => {
    mockDelete.mockResolvedValue({ message: 'ok' });
    await deleteSchedule('panel', row.key);
    expect(mockDelete).toHaveBeenCalledWith(`${RESOURCE}?scope=panel&key=${row.key}`);
  });

  it('deletes the stored model with drop=model', async () => {
    mockDelete.mockResolvedValue({ message: 'ok' });
    await deleteScheduleModel('panel', row.key);
    expect(mockDelete).toHaveBeenCalledWith(`${RESOURCE}?scope=panel&key=${row.key}&drop=model`);
  });

  it('posts the default schedule', async () => {
    mockPost.mockResolvedValue({ cron: '0 4 * * *', timezone: 'Europe/Moscow' });
    await postScheduleDefault({ cron: '0 4 * * *', timezone: 'Europe/Moscow' });
    expect(mockPost).toHaveBeenCalledWith(`${RESOURCE}/default`, { cron: '0 4 * * *', timezone: 'Europe/Moscow' });
  });
});
