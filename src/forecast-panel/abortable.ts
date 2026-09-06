import { getBackendSrv } from '@grafana/runtime';
import { Observable } from 'rxjs';

export function abortError(): Error {
  const err = new Error('Aborted');
  err.name = 'AbortError';
  return err;
}

/**
 * Last value of an Observable as a Promise, unsubscribing when `signal` aborts.
 *
 * Grafana's `getBackendSrv().fetch` and `DataSourceApi.query` build on RxJS
 * `fromFetch`, which aborts the underlying HTTP request on unsubscribe. So an
 * aborted overlay load frees the backend inflight slot (the Go handler sees
 * `context.Canceled`) instead of only skipping the *next* request.
 */
export function abortableLastValue<T>(source: Observable<T>, signal?: AbortSignal): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError());
      return;
    }
    let last: T | undefined;
    let hasValue = false;
    let settled = false;
    const onAbort = () => {
      if (settled) {
        return;
      }
      settled = true;
      sub.unsubscribe();
      reject(abortError());
    };
    const done = () => {
      settled = true;
      signal?.removeEventListener('abort', onAbort);
    };
    const sub = source.subscribe({
      next: (v) => {
        last = v;
        hasValue = true;
      },
      error: (e) => {
        if (settled) {
          return;
        }
        done();
        reject(e);
      },
      complete: () => {
        if (settled) {
          return;
        }
        done();
        if (hasValue) {
          resolve(last as T);
        } else {
          reject(new Error('no elements in sequence'));
        }
      },
    });
    if (settled) {
      // Source completed synchronously.
      return;
    }
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

/** POST a plugin resource; the request is cancelled when `signal` aborts. */
export function postResource<T>(url: string, body: unknown, signal?: AbortSignal): Promise<T> {
  return abortableLastValue(getBackendSrv().fetch<T>({ url, method: 'POST', data: body }), signal).then(
    (resp) => resp.data
  );
}
