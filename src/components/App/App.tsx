import React from 'react';
import { Route, Routes } from 'react-router-dom';
import { AppRootProps } from '@grafana/data';
import Home from '../../pages/Home';

function App(_props: AppRootProps) {
  return (
    <Routes>
      <Route path="*" element={<Home />} />
    </Routes>
  );
}

export default App;
