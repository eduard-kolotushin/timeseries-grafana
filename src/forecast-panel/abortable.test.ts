import { Observable, of, throwError } from 'rxjs';
import { abortableLastValue, postResource } from './abortable';

const fetchMock = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ fetch: fetchMock }),
}));

describe('abortableLastValue', () => {
  it('resolves with the last emitted value', async () => {
    await expect(abortableLastValue(of(1, 2, 3))).resolves.toBe(3);
  });

  it('rejects with the source error', async () => {
    await expect(abortableLastValue(throwError(() => ({ status: 429 })))).rejects.toEqual({ status: 429 });
  });

  it('rejects immediately when the signal is already aborted and never subscribes', async () => {
    const ac = new AbortController();
    ac.abort();
    const subscribe = jest.fn();
    const source = new Observable<number>(subscribe);
    await expect(abortableLastValue(source, ac.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(subscribe).not.toHaveBeenCalled();
  });

  it('unsubscribes the in-flight source and rejects AbortError on abort', async () => {
    const teardown = jest.fn();
    const source = new Observable<number>(() => teardown);
    const ac = new AbortController();
    const p = abortableLastValue(source, ac.signal);
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
    expect(teardown).toHaveBeenCalledTimes(1);
  });

  it('ignores a late abort after completion', async () => {
    const ac = new AbortController();
    const got = await abortableLastValue(of('done'), ac.signal);
    ac.abort();
    expect(got).toBe('done');
  });
});

describe('postResource', () => {
  beforeEach(() => fetchMock.mockReset());

  it('returns response.data', async () => {
    fetchMock.mockReturnValue(of({ data: { times: [1], values: [2] } }));
    await expect(postResource('/x', { a: 1 })).resolves.toEqual({ times: [1], values: [2] });
    expect(fetchMock).toHaveBeenCalledWith({ url: '/x', method: 'POST', data: { a: 1 } });
  });

  it('cancels the HTTP request when the load is aborted', async () => {
    const teardown = jest.fn();
    fetchMock.mockReturnValue(new Observable(() => teardown));
    const ac = new AbortController();
    const p = postResource('/x', {}, ac.signal);
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
    expect(teardown).toHaveBeenCalledTimes(1);
  });
});
