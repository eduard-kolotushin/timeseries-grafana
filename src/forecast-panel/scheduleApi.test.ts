import { deleteSchedule, listSchedules, postScheduleDefault, putSchedule, ScheduleRow } from './scheduleApi';

const mockGet = jest.fn();
const mockPut = jest.fn();
const mockDelete = jest.fn();
const mockPost = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ get: mockGet, put: mockPut, delete: mockDelete, post: mockPost }),
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
    mockPut.mockReset();
    mockDelete.mockReset();
    mockPost.mockReset();
  });

  it('lists the org schedules', async () => {
    mockGet.mockResolvedValue([row]);
    await expect(listSchedules()).resolves.toEqual([row]);
    expect(mockGet).toHaveBeenCalledWith(RESOURCE);
  });

  it('upserts a row', async () => {
    mockPut.mockResolvedValue(row);
    const edited = { ...row, cron: '*/2 * * * *', enabled: false };
    await putSchedule(edited);
    expect(mockPut).toHaveBeenCalledWith(RESOURCE, edited);
  });

  it('deletes by scope and key query params', async () => {
    mockDelete.mockResolvedValue({ message: 'ok' });
    await deleteSchedule('panel', row.key);
    expect(mockDelete).toHaveBeenCalledWith(`${RESOURCE}?scope=panel&key=${row.key}`);
  });

  it('posts the default schedule', async () => {
    mockPost.mockResolvedValue({ cron: '0 4 * * *', timezone: 'Europe/Moscow' });
    await postScheduleDefault({ cron: '0 4 * * *', timezone: 'Europe/Moscow' });
    expect(mockPost).toHaveBeenCalledWith(`${RESOURCE}/default`, { cron: '0 4 * * *', timezone: 'Europe/Moscow' });
  });
});
