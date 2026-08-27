import React from 'react';
import { StandardEditorProps } from '@grafana/data';
import { getAppEvents } from '@grafana/runtime';
import { Button } from '@grafana/ui';
import { queueRetrainAll } from './retrain';

export const RetrainEditor: React.FC<StandardEditorProps> = () => {
  return (
    <Button
      variant="secondary"
      onClick={() => {
        queueRetrainAll();
        getAppEvents().publish({ type: 'refresh' } as never);
      }}
    >
      Retrain
    </Button>
  );
};
