import { DataQueryResponseData, FieldType, toDataFrame } from '@grafana/data';
import { toTrainingFrames } from './trainFrames';

/** What `sbt-datasource-abyss` answers for a `format: "table"` target: no `fields`, a time column of local date strings. */
const abyssTable = {
  columns: [{ text: 'time', type: 'time' }, { text: 'avg' }],
  rows: [
    ['2026-10-07 18:00:00', 13319.2099],
    ['2026-10-07 19:00:00', 1.5],
  ],
  meta: { executedQueryString: 'SELECT …' },
  refId: 'A',
  type: 'table',
};

/** The same table, minus the column types an Abyss-style datasource does not always send. */
const untypedTable = {
  ...abyssTable,
  columns: [{ text: 'time' }, { text: 'avg' }],
};

/** What the Abyss object above has to become: the datasource renders `toUtc(iso).local()`, so local ms. */
const abyssFrame = {
  refId: 'A',
  fields: [
    {
      name: 'time',
      type: FieldType.time,
      values: [new Date(2026, 9, 7, 18, 0, 0).getTime(), new Date(2026, 9, 7, 19, 0, 0).getTime()],
    },
    { name: 'avg', type: FieldType.number, values: [13319.2099, 1.5] },
  ],
};

type Case = {
  name: string;
  data: DataQueryResponseData[];
  want: Array<{
    refId?: string;
    fields: Array<{ name: string; type: FieldType; values: unknown[] }>;
  }>;
};

const cases: Case[] = [
  {
    name: 'keeps a real frame, values and types untouched',
    data: [
      toDataFrame({
        refId: 'A',
        fields: [
          { name: 'Time', type: FieldType.time, values: [1000, 2000] },
          { name: 'up', type: FieldType.number, values: [1, 2] },
        ],
      }),
    ],
    want: [
      {
        refId: 'A',
        fields: [
          { name: 'Time', type: FieldType.time, values: [1000, 2000] },
          { name: 'up', type: FieldType.number, values: [1, 2] },
        ],
      },
    ],
  },
  {
    name: 'converts an Abyss table object whose time column is a local date string',
    data: [abyssTable],
    want: [abyssFrame],
  },
  {
    name: 'types an untyped legacy table the same way',
    data: [untypedTable],
    want: [abyssFrame],
  },
  {
    name: 'leaves a string column a string',
    data: [
      {
        columns: [{ text: 'time', type: 'time' }, { text: 'name' }],
        rows: [['2026-10-07 18:00:00', 'jvm_heap']],
        type: 'table',
      },
    ],
    want: [
      {
        fields: [
          { name: 'time', type: FieldType.time, values: [new Date(2026, 9, 7, 18, 0, 0).getTime()] },
          { name: 'name', type: FieldType.string, values: ['jvm_heap'] },
        ],
      },
    ],
  },
  {
    name: 'converts a graphite series',
    data: [{ target: 'x', datapoints: [[1, 1000], [2, 2000]] }],
    want: [
      {
        fields: [
          { name: 'Time', type: FieldType.time, values: [1000, 2000] },
          { name: 'Value', type: FieldType.number, values: [1, 2] },
        ],
      },
    ],
  },
  { name: 'returns nothing for an empty reply', data: [], want: [] },
  {
    name: 'drops an unreadable item and keeps the rest',
    data: [{ nonsense: true }, abyssTable],
    want: [abyssFrame],
  },
];

describe('toTrainingFrames', () => {
  it.each(cases)('$name', ({ data, want }) => {
    const frames = toTrainingFrames(data);
    expect(
      frames.map((frame) => ({
        refId: frame.refId,
        fields: frame.fields.map((field) => ({ name: field.name, type: field.type, values: field.values })),
      }))
    ).toEqual(want);
  });
});
