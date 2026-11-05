import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Brain, CaretDown, CheckCircle, CircleNotch, Stop, WarningCircle } from '@phosphor-icons/react';
import { currentSubagentActivity, formatSubagentElapsed, subagentActivities } from './subagent-progress.js';
import MarkdownContent from './markdown-content.js';
import './subagent-run.css';

function Elapsed({ start, end, live = false }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!live) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [live, start]);
  const elapsed = formatSubagentElapsed(start, live ? now : end);
  return elapsed ? <span className="subagent-elapsed" aria-label={`Elapsed time ${elapsed}`}>{elapsed}</span> : null;
}

function ActionIcon({ status }) {
  const Icon = status === 'running' ? CircleNotch : status === 'complete' ? CheckCircle : status === 'failed' || status === 'error' ? WarningCircle : Stop;
  return <Icon size={17} aria-hidden="true" />;
}

function ActivityRow({ activity }) {
  const live = activity.status === 'running';
  const label = live ? 'In progress' : activity.status === 'complete' ? 'Done' : activity.status === 'failed' || activity.status === 'error' ? 'Failed' : 'Stopped';
  const hasDetails = Boolean(activity.detail || activity.output);
  return <li className={`subagent-action ${activity.status}`}>
    <span className="subagent-action-icon"><ActionIcon status={activity.status} /></span>
    <details className="subagent-action-detail">
      <summary aria-label={`${activity.title} · ${label}${hasDetails ? ' · Show details' : ''}`}>
        <span className="subagent-action-copy"><span className="subagent-action-title">{activity.title}</span>{activity.tool_name === 'shell' && activity.detail && <span className="subagent-command-preview">{activity.detail}</span>}</span>
        <span className="subagent-action-state">{label}</span>
        <Elapsed start={activity.started_at} end={activity.completed_at} live={live} />
        <CaretDown className="subagent-action-chevron" size={13} aria-hidden="true" />
      </summary>
      <div className="subagent-action-output">
        {activity.detail && <pre tabIndex={0} aria-label="Action details">{activity.detail}</pre>}
        {activity.output && <><span className="subagent-output-label">Output excerpt</span><pre tabIndex={0} aria-label="Action output excerpt">{activity.output}</pre></>}
        {!hasDetails && <p>{label}.</p>}
      </div>
    </details>
  </li>;
}

export default function SubagentRun({ run }) {
  const live = run.status === 'running' || run.status === 'queued';
  const [expanded, setExpanded] = useState(live);
  useEffect(() => { if (live) setExpanded(true); }, [live]);
  const [showAll, setShowAll] = useState(false);
  const [showFullTask, setShowFullTask] = useState(false);
  const [taskTruncated, setTaskTruncated] = useState(false);
  const goalRef = useRef(null);
  useLayoutEffect(() => {
    const goal = goalRef.current;
    if (!goal) return;
    const measure = () => setTaskTruncated(goal.scrollHeight > goal.clientHeight + 1);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(goal);
    return () => observer.disconnect();
  }, [run.task, showFullTask]);
  const status = run.status === 'running' ? 'Working' : run.status === 'queued' ? 'Waiting' : run.status === 'complete' ? 'Complete' : run.status === 'failed' ? 'Failed' : 'Stopped';
  const activities = subagentActivities(run);
  const shown = showAll ? activities : activities.slice(-4);
  const completed = activities.filter((item) => item.status === 'complete').length;
  const current = currentSubagentActivity(run);
  return <details className={`subagent-run ${run.status}`} open={expanded} onToggle={(event) => setExpanded(event.currentTarget.open)}>
    <summary className="subagent-run-heading">
      <Brain size={18} aria-hidden="true" />
      <span className="subagent-run-identity">Subagent<span className="subagent-collapsed-task"> · {run.task || 'Delegated task'}</span></span>
      <span className="subagent-run-status" role="status" aria-live="polite">{status}</span>
      <Elapsed start={run.started_at || run.created_at} end={run.completed_at} live={live} />
      <CaretDown className="subagent-run-chevron" size={15} aria-hidden="true" />
    </summary>
    <div className="subagent-run-body">
      <p ref={goalRef} className={`subagent-goal ${showFullTask ? 'expanded' : ''}`}>{run.task || 'Delegated task'}</p>
      {(taskTruncated || showFullTask) && <button type="button" className="subagent-task-toggle" aria-expanded={showFullTask} onClick={() => setShowFullTask((value) => !value)}>{showFullTask ? 'Show less' : 'Show full task'}</button>}
      {live && <div className="subagent-current">
        <CircleNotch size={18} aria-hidden="true" />
        <div>
          <span className="subagent-section-label">{run.status === 'queued' ? 'In the queue' : 'Now'}</span>
          <p className="subagent-current-title" role="status" aria-live="polite">{current.title}</p>
          {current.detail && <p className="subagent-current-detail">{current.detail}</p>}
        </div>
      </div>}
      {activities.length > 0 && <section className="subagent-work" aria-label="Subagent activity">
        <div className="subagent-work-heading"><h4>Recent activity</h4><span>{completed} completed</span></div>
        <ol className="subagent-action-list">{shown.map((activity) => <ActivityRow key={activity.id} activity={activity} />)}</ol>
        {activities.length > 4 && <button type="button" className="subagent-history-toggle" onClick={() => setShowAll((value) => !value)}>{showAll ? 'Show recent activity' : `Show all ${activities.length} recent actions`}</button>}
      </section>}
      {live && activities.length === 0 && <p className="subagent-empty-activity">File reads, searches, and other actions will appear here as they happen.</p>}
      {run.progress?.total > 0 && <p className="subagent-batch-progress">{run.progress.processed} of {run.progress.total} images processed · {run.progress.failed || 0} failed · {run.progress.needs_review || 0} need review</p>}
      {run.result_summary && <section className="subagent-result"><h4>Result</h4><div className="markdown-content"><MarkdownContent>{run.result_summary}</MarkdownContent></div></section>}
      {run.error && <p className="subagent-run-error"><WarningCircle size={16} aria-hidden="true" />{run.error}</p>}
      {run.output_refs?.length > 0 && <div className="subagent-output-refs"><h4>Files</h4><ul>{run.output_refs.map((path) => <li key={path}><code>{path}</code></li>)}</ul></div>}
      {!live && activities.length === 0 && !run.result_summary && !run.error && <p className="subagent-empty-activity">{status}. No action details were saved for this run.</p>}
    </div>
  </details>;
}
