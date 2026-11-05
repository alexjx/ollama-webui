import { subagentPhaseLabel } from './agent-events.js';

export function subagentActivities(run) {
  const activities = run.progress?.activities;
  if (!Array.isArray(activities)) return [];
  return activities.filter((item) => item && typeof item.title === 'string').slice(-12).map((item) => {
    if (item.status !== 'running' || run.status === 'running') return item;
    return { ...item, status: run.status === 'failed' ? 'failed' : 'cancelled', completed_at: run.completed_at };
  });
}

export function currentSubagentActivity(run) {
  const activity = subagentActivities(run).findLast((item) => item.status === 'running');
  if (!activity && run.status === 'running' && run.progress?.total > 0) {
    return { title: 'Processing images', detail: `${run.progress.processed || 0} of ${run.progress.total} images processed` };
  }
  return activity || { title: subagentPhaseLabel(run), detail: '' };
}

export function formatSubagentElapsed(start, end) {
  const from = Date.parse(start);
  const to = typeof end === 'number' ? end : Date.parse(end);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to < from) return null;
  const seconds = Math.floor((to - from) / 1000);
  if (seconds < 1) return '<1s';
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`;
}
