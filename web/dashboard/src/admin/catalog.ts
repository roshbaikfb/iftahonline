import { useCallback, useEffect, useState } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import type { Feature, Plan } from './plans';

function fetchCatalog() {
  return Promise.all([api.GET('/admin/v1/plans'), api.GET('/admin/v1/features')]);
}

// useCatalog reads the plans and the features catalog, again on reload.
export function useCatalog() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchCatalog>>) => {
    const [p, f] = r;
    if (p.data) setPlans(p.data.items);
    if (f.data) setFeatures(f.data.items);
    const err = p.error ?? f.error;
    setError(err ? problemMessage(err) : '');
    setLoaded(true);
  }, []);

  useEffect(() => {
    let live = true;
    void fetchCatalog().then((r) => {
      if (live) apply(r);
    });
    return () => {
      live = false;
    };
  }, [apply]);

  const reload = useCallback(async () => {
    apply(await fetchCatalog());
  }, [apply]);
  return { plans, features, loaded, error, setError, reload };
}

