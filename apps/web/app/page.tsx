"use client";
import { useCallback, useEffect, useRef, useState } from "react";

type Project = { id: string; name: string; archived_at: string | null };
type Endpoint = {
  id: string;
  url: string;
  description: string;
  paused: boolean;
  version: number;
};
type Key = {
  id: string;
  name: string;
  prefix: string;
  revoked_at: string | null;
  expires_at: string | null;
};
type Event = { id: string; type: string; status: string; accepted_at: string };
type Delivery = {
  id: string;
  endpoint_id: string;
  state: string;
  generation: number;
  attempts_count: number;
  last_error: string;
  endpoint_version: number;
  deadline_at: string;
};
type Attempt = {
  id: string;
  sequence: number;
  outcome: string;
  http_status: number | null;
  started_at: string;
  duration_ms: number | null;
  error_class: string;
  response_preview: string;
};
type Detail = {
  event: Event;
  envelope: unknown;
  deliveries: Delivery[];
  payload_purged_at: string | null;
};
type Me = {
  user: { email: string };
  organization: { name: string; id: string };
  role: string;
};
type Usage = {
  accepted_event_count: number;
  reset_at: string;
  limits: { events_per_month: number };
};
type List<T> = { data: T[]; next_cursor: string | null };
type Tab = "Overview" | "Events" | "Endpoints" | "API keys" | "Project";
const tabs: Tab[] = ["Overview", "Events", "Endpoints", "API keys", "Project"];
const short = (s: string) => s.slice(0, 8);
const stamp = (s: string) => new Date(s).toLocaleString();
function Shield() {
  return (
    <svg
      width="28"
      height="30"
      viewBox="0 0 28 30"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M14 2 25 6v9c0 6-5 10-11 13C8 25 3 21 3 15V6L14 2Z"
        stroke="currentColor"
        strokeWidth="2"
      />
      <path
        d="m9 14 4 4 7-8"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
function Badge({ value }: { value: string }) {
  return <span className={"badge " + value}>{value.replaceAll("_", " ")}</span>;
}
export default function Console() {
  const [me, setMe] = useState<Me | null>(null),
    [checking, setChecking] = useState(true),
    [csrf, setCSRF] = useState(""),
    [tab, setTab] = useState<Tab>("Overview");
  const [projects, setProjects] = useState<Project[]>([]),
    [project, setProject] = useState(""),
    [endpoints, setEndpoints] = useState<Endpoint[]>([]),
    [keys, setKeys] = useState<Key[]>([]),
    [events, setEvents] = useState<Event[]>([]),
    [usage, setUsage] = useState<Usage | null>(null);
  const [detail, setDetail] = useState<Detail | null>(null),
    [attempts, setAttempts] = useState<Record<string, Attempt[]>>({}),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [busy, setBusy] = useState(false),
    [secret, setSecret] = useState("");
  const [status, setStatus] = useState(""),
    [kind, setKind] = useState(""),
    [from, setFrom] = useState(""),
    [to, setTo] = useState(""),
    [next, setNext] = useState<string | null>(null),
    [showProject, setShowProject] = useState(false);
  const [edit, setEdit] = useState<Endpoint | null>(null),
    [publish, setPublish] = useState(false),
    [receipt, setReceipt] = useState<unknown>(null);
  const modalRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!publish) return;
    const previous = document.activeElement as HTMLElement;
    const modal = modalRef.current;
    const elements = () =>
      Array.from(
        modal?.querySelectorAll<HTMLElement>(
          'button:not([disabled]),input,textarea,select,[tabindex="0"]',
        ) || [],
      );
    elements()[0]?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setPublish(false);
        return;
      }
      if (event.key === "Tab") {
        const list = elements();
        const first = list[0],
          last = list[list.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      previous?.focus();
    };
  }, [publish]);
  const currentProject = projects.find((p) => p.id === project);
  const serial = useRef(0);
  const api = useCallback(
    async <T,>(
      path: string,
      method = "GET",
      body?: unknown,
      extra?: Record<string, string>,
    ): Promise<T> => {
      const headers: Record<string, string> = { ...extra };
      if (body !== undefined) headers["Content-Type"] = "application/json";
      if (method !== "GET") headers["X-CSRF-Token"] = csrf;
      const response = await fetch("/api" + path, {
        method,
        credentials: "same-origin",
        cache: "no-store",
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (response.status === 204) return undefined as T;
      const result = await response.json();
      if (!response.ok) {
        if (response.status === 401 && path != "/v1/auth/login") setMe(null);
        throw new Error(result.error?.message || "Request failed");
      }
      return result as T;
    },
    [csrf],
  );
  const all = useCallback(
    async <T,>(path: string): Promise<T[]> => {
      let data: T[] = [];
      let cursor: string | null = null;
      do {
        const result: List<T> = await api(
          path +
            (path.includes("?") ? "&" : "?") +
            "limit=100" +
            (cursor ? "&cursor=" + encodeURIComponent(cursor) : ""),
        );
        data = data.concat(result.data);
        cursor = result.next_cursor;
      } while (cursor);
      return data;
    },
    [api],
  );
  const loadProjects = useCallback(async () => {
    const data = await all<Project>("/v1/projects");
    setProjects(data);
    setProject((old) =>
      data.some((p) => p.id === old) ? old : data[0]?.id || "",
    );
  }, [all]);
  const load = useCallback(
    async (cursor?: string) => {
      if (!project) return;
      const token = ++serial.current;
      const q = new URLSearchParams({ limit: "25" });
      if (status) q.set("status", status);
      if (kind) q.set("type", kind);
      if (from) q.set("from", new Date(from).toISOString());
      if (to) q.set("to", new Date(to).toISOString());
      if (cursor) q.set("cursor", cursor);
      const [e, k, v, u] = await Promise.all([
        all<Endpoint>(`/v1/projects/${project}/endpoints`),
        all<Key>(`/v1/projects/${project}/keys`),
        api<List<Event>>(`/v1/projects/${project}/events?${q}`),
        api<Usage>("/v1/usage"),
      ]);
      if (token !== serial.current) return;
      setEndpoints(e);
      setKeys(k);
      setEvents((old) => (cursor ? [...old, ...v.data] : v.data));
      setNext(v.next_cursor);
      setUsage(u);
    },
    [project, status, kind, from, to, all, api],
  );
  useEffect(() => {
    let mounted = true;
    api<Me>("/v1/me")
      .then(async (m) => {
        const c = await api<{ csrf_token: string }>("/v1/auth/csrf");
        if (mounted) {
          setMe(m);
          setCSRF(c.csrf_token);
        }
      })
      .catch(() => {})
      .finally(() => {
        if (mounted) setChecking(false);
      });
    return () => {
      mounted = false;
    };
  }, []); // Session check runs once on mount.
  useEffect(() => {
    if (me) loadProjects().catch((e) => setError(e.message));
  }, [me, loadProjects]);
  useEffect(() => {
    if (!me || !project) return;
    setDetail(null);
    setAttempts({});
    load().catch((e) => setError(e.message));
    const timer = setInterval(() => {
      if (!document.hidden) load().catch((e) => setError(e.message));
    }, 10000);
    return () => {
      clearInterval(timer);
      serial.current++;
    };
  }, [me, project, load]);
  async function action(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }
  async function openEvent(id: string) {
    await action(async () => {
      setDetail(await api<Detail>(`/v1/projects/${project}/events/${id}`));
      setAttempts({});
    });
  }
  async function refresh() {
    await loadProjects();
    if (project) await load();
  }
  function fields(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    return new FormData(event.currentTarget);
  }
  if (checking)
    return (
      <main className="loading">
        <Shield />
        <p>Opening delivery console…</p>
      </main>
    );
  if (!me)
    return (
      <main className="login-shell">
        <section className="login-story">
          <div className="brand">
            <Shield /> hookgarde<span>SELF-HOSTED</span>
          </div>
          <div>
            <p className="eyebrow">A clear path from event to endpoint</p>
            <h1>
              Webhooks you
              <br />
              can account for.
            </h1>
            <p>
              Publish once. Inspect every attempt.
              <br />
              Recover with confidence.
            </p>
            <div className="delivery-line">
              <span>Event accepted</span>
              <i />
              <span>Signed delivery</span>
              <i />
              <span>Full history</span>
            </div>
          </div>
          <small>Reliable webhooks. Zero infrastructure headaches.</small>
        </section>
        <section className="login-form">
          <p className="eyebrow">YOUR DELIVERY WORKSPACE</p>
          <h2>Welcome back</h2>
          <p className="muted">
            Sign in with your operator-provisioned account.
          </p>
          <form
            onSubmit={(event) => {
              const f = fields(event);
              action(async () => {
                const m = await api<Me>("/v1/auth/login", "POST", {
                  email: f.get("email"),
                  password: f.get("password"),
                });
                const c = await api<{ csrf_token: string }>("/v1/auth/csrf");
                setCSRF(c.csrf_token);
                setMe(m);
              });
            }}
          >
            <label>
              Email address
              <input
                name="email"
                type="email"
                autoComplete="username"
                required
                placeholder="you@company.com"
              />
            </label>
            <label>
              Password
              <input
                name="password"
                type="password"
                minLength={12}
                maxLength={128}
                autoComplete="current-password"
                required
              />
            </label>
            {error && (
              <p role="alert" className="error">
                {error}
              </p>
            )}
            <button disabled={busy} className="primary">
              {busy ? "Signing in…" : "Sign in to Hookgarde →"}
            </button>
          </form>
          <p className="hint">
            No account? Ask your installation operator to provision an owner
            using the admin command.
          </p>
        </section>
      </main>
    );
  return (
    <div className="app-shell">
      <aside>
        <div className="brand">
          <Shield /> hookgarde
        </div>
        <div className="workspace">
          <span className="workspace-icon">
            {me.organization.name[0]?.toUpperCase()}
          </span>
          <div>
            <strong>{me.organization.name}</strong>
            <small>Self-hosted workspace</small>
          </div>
          <span className="live-dot" />
        </div>
        <p className="nav-label">WORKSPACE</p>
        <nav aria-label="Main navigation">
          {tabs.map((item, i) => (
            <button
              key={item}
              onClick={() => {
                setTab(item);
                setDetail(null);
              }}
              className={tab === item ? "selected" : ""}
            >
              <span className="nav-symbol" aria-hidden="true">
                {["◈", "↗", "◎", "⚿", "⚙"][i]}
              </span>
              {item}
              {item === "Endpoints" && (
                <span className="nav-count" aria-hidden="true">
                  {endpoints.length}
                </span>
              )}
            </button>
          ))}
        </nav>
        <div className="aside-bottom">
          <div className="local-note">
            <span className="live-dot" /> SELF-HOSTED CORE
            <p>
              Your infrastructure.
              <br />
              Your delivery history.
            </p>
          </div>
          <p className="account" title={me.user.email}>
            {me.user.email}
          </p>
          <button
            className="text-button"
            disabled={busy}
            onClick={() =>
              action(async () => {
                await api("/v1/auth/logout", "POST");
                setMe(null);
                setCSRF("");
                setSecret("");
              })
            }
          >
            Sign out ↗
          </button>
        </div>
      </aside>
      <div className="main-shell">
        <header>
          <div className="breadcrumb">
            Workspace <span>/</span> <strong>{tab}</strong>
          </div>
          <div className="header-right">
            <span className="environment">● SELF-HOSTED</span>
            <button
              aria-label="Refresh data"
              onClick={() => action(refresh)}
              disabled={busy}
            >
              ↻ Refresh
            </button>
          </div>
        </header>
        <main className="content">
          <div className="page-title">
            <div>
              <p className="eyebrow">HOOKGARDE CONSOLE</p>
              <h1>
                {detail
                  ? "Event details"
                  : tab === "Overview"
                    ? "Delivery overview"
                    : tab}
              </h1>
              <p className="muted">
                {detail
                  ? "Follow the event through every delivery generation."
                  : tab === "Overview"
                    ? "A little more certainty for every event you send."
                    : tab === "Events"
                      ? "Find an event. Follow its attempts. Understand the outcome."
                      : tab === "Endpoints"
                        ? "Manage where your events go and when delivery runs."
                        : tab === "API keys"
                          ? "Project-scoped credentials for publishing events."
                          : "Manage your project and its lifecycle."}
              </p>
            </div>
            <button className="primary" onClick={() => setShowProject(true)}>
              ＋ New project
            </button>
          </div>
          <div className="project-bar">
            <label>
              PROJECT
              <select
                aria-label="Project"
                value={project}
                onChange={(e) => {
                  setProject(e.target.value);
                  setSecret("");
                  setReceipt(null);
                }}
              >
                <option value="" disabled>
                  Select a project
                </option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                    {p.archived_at ? " (archived)" : ""}
                  </option>
                ))}
              </select>
            </label>
            <span className="mono">{project || "No project selected"}</span>
            {currentProject?.archived_at && <Badge value="archived" />}
            <span className="auto-refresh">Updates every 10 seconds</span>
          </div>
          {error && (
            <div className="error" role="alert">
              {error}
              <button aria-label="Dismiss error" onClick={() => setError("")}>
                ×
              </button>
            </div>
          )}
          {notice && (
            <div className="notice" role="status">
              {notice}
            </div>
          )}
          {secret && (
            <section className="secret-box" aria-label="One-time secret">
              <div>
                <strong>Save this secret now</strong>
                <p>
                  This value is shown once. Store it securely before closing.
                </p>
              </div>
              <pre>{secret}</pre>
              <button
                onClick={() => {
                  setSecret("");
                  setNotice("Secret dismissed. It cannot be retrieved again.");
                }}
              >
                I saved it · dismiss
              </button>
            </section>
          )}
          {showProject && (
            <section className="panel">
              <div className="panel-heading">
                <h2>Create project</h2>
                <button onClick={() => setShowProject(false)}>Cancel</button>
              </div>
              <form
                className="inline-form"
                onSubmit={(event) => {
                  const f = fields(event);
                  action(async () => {
                    const p = await api<Project>("/v1/projects", "POST", {
                      name: f.get("name"),
                    });
                    await loadProjects();
                    setProject(p.id);
                    setShowProject(false);
                    setNotice(
                      "Project created. Add an endpoint and an ingestion key next.",
                    );
                  });
                }}
              >
                <label>
                  Project name
                  <input
                    name="name"
                    required
                    maxLength={120}
                    placeholder="Acme production"
                  />
                </label>
                <button disabled={busy} className="primary">
                  Create project
                </button>
              </form>
            </section>
          )}
          {!project ? (
            <section className="empty panel">
              <span className="empty-symbol">↗</span>
              <h2>Your first event starts here</h2>
              <p>
                Create a project to organize endpoints, ingestion keys and
                delivery history.
              </p>
              <button className="primary" onClick={() => setShowProject(true)}>
                Create your first project
              </button>
            </section>
          ) : detail ? (
            <>
              <button className="text-button" onClick={() => setDetail(null)}>
                ← Back to {tab.toLowerCase()}
              </button>
              <section className="panel detail-head">
                <div>
                  <span className="eyebrow">EVENT</span>
                  <h2>{detail.event.type}</h2>
                  <code>{detail.event.id}</code>
                </div>
                <Badge value={detail.event.status} />
                <button
                  onClick={() => openEvent(detail.event.id)}
                  disabled={busy}
                >
                  Refresh event
                </button>
              </section>
              <section className="panel">
                <div className="panel-heading">
                  <h2>Immutable event envelope</h2>
                  <span className="muted">
                    {stamp(detail.event.accepted_at)}
                  </span>
                </div>
                <pre className="payload">
                  {detail.envelope
                    ? JSON.stringify(detail.envelope, null, 2)
                    : "Payload expired. Delivery metadata remains available."}
                </pre>
              </section>
              <h2 className="section-label">
                Delivery generations <span>{detail.deliveries.length}</span>
              </h2>
              {detail.deliveries.map((d) => (
                <section className="panel delivery-card" key={d.id}>
                  <div className="delivery-heading">
                    <div>
                      <strong>
                        {endpoints.find((e) => e.id === d.endpoint_id)
                          ?.description || "Endpoint " + short(d.endpoint_id)}
                      </strong>
                      <p className="mono muted">{d.id}</p>
                    </div>
                    <Badge value={d.state} />
                    <span className="muted">
                      Generation {d.generation} · {d.attempts_count}/6 attempts
                    </span>
                  </div>
                  <p className="muted">
                    URL version {d.endpoint_version} · Deadline{" "}
                    {stamp(d.deadline_at)}
                    {endpoints.find((e) => e.id === d.endpoint_id)?.paused
                      ? " · Endpoint paused"
                      : ""}
                  </p>
                  {d.last_error && <p className="error-text">{d.last_error}</p>}
                  <div className="button-row">
                    <button
                      disabled={busy}
                      onClick={() =>
                        action(async () => {
                          const v = await all<Attempt>(
                            `/v1/projects/${project}/deliveries/${d.id}/attempts`,
                          );
                          setAttempts((old) => ({ ...old, [d.id]: v }));
                        })
                      }
                    >
                      Inspect attempts
                    </button>
                    {["succeeded", "failed", "canceled"].includes(d.state) && (
                      <button
                        disabled={
                          busy ||
                          !!currentProject?.archived_at ||
                          !detail.envelope
                        }
                        onClick={() =>
                          action(async () => {
                            await api(
                              `/v1/projects/${project}/deliveries/${d.id}/replay`,
                              "POST",
                              {},
                              { "Idempotency-Key": crypto.randomUUID() },
                            );
                            setDetail(
                              await api(
                                `/v1/projects/${project}/events/${detail.event.id}`,
                              ),
                            );
                            setNotice(
                              "Replay accepted as a new delivery generation.",
                            );
                            await load();
                          })
                        }
                      >
                        ↻ Replay delivery
                      </button>
                    )}
                  </div>
                  {attempts[d.id] && (
                    <div className="timeline">
                      {attempts[d.id].length === 0 ? (
                        <p className="muted">No attempt has started yet.</p>
                      ) : (
                        attempts[d.id].map((a) => (
                          <div className="attempt" key={a.id}>
                            <span className="attempt-number">{a.sequence}</span>
                            <div>
                              <Badge value={a.outcome} />
                              <span className="muted">
                                {" "}
                                {stamp(a.started_at)} ·{" "}
                                {a.http_status ?? "No response"} ·{" "}
                                {a.duration_ms ?? "—"} ms
                              </span>
                              {a.error_class && <p>{a.error_class}</p>}
                              {a.response_preview && (
                                <pre>{a.response_preview}</pre>
                              )}
                            </div>
                          </div>
                        ))
                      )}
                    </div>
                  )}
                </section>
              ))}
            </>
          ) : (
            <>
              {(tab === "Overview" || tab === "Events") && (
                <>
                  {tab === "Overview" && (
                    <>
                      <div className="stat-grid">
                        <div className="stat">
                          <span>Accepted this month</span>
                          <strong>
                            {usage?.accepted_event_count.toLocaleString() ??
                              "—"}
                          </strong>
                          <small>
                            of{" "}
                            {usage?.limits.events_per_month.toLocaleString() ??
                              "—"}{" "}
                            configured events
                          </small>
                        </div>
                        <div className="stat">
                          <span>Active endpoints</span>
                          <strong>
                            {endpoints.filter((e) => !e.paused).length}
                            <em> / {endpoints.length}</em>
                          </strong>
                          <small>
                            {endpoints.filter((e) => e.paused).length} paused
                          </small>
                        </div>
                        <div className="stat">
                          <span>In this event page</span>
                          <strong>
                            {
                              events.filter((e) => e.status === "pending")
                                .length
                            }
                            <em> pending</em>
                          </strong>
                          <small>
                            Showing {events.length} events · use filters to
                            investigate
                          </small>
                        </div>
                      </div>
                      <section className="getting-started">
                        <span className="getting-icon">↗</span>
                        <div>
                          <h2>From published to accounted for.</h2>
                          <p>
                            Create an endpoint, save its signing secret, then
                            publish with a project key.
                          </p>
                        </div>
                        <button
                          onClick={() =>
                            setTab(endpoints.length ? "API keys" : "Endpoints")
                          }
                        >
                          Set up delivery →
                        </button>
                      </section>
                    </>
                  )}
                  <section className="panel">
                    <div className="panel-heading">
                      <div>
                        <h2>
                          {tab === "Overview"
                            ? "Recent events"
                            : "Event history"}
                        </h2>
                        <p className="muted">
                          Acceptance is durable. A 2xx records receiver
                          transport acceptance.
                        </p>
                      </div>
                      <button
                        disabled={!!currentProject?.archived_at}
                        className="primary"
                        onClick={() => {
                          setPublish(true);
                          setReceipt(null);
                        }}
                      >
                        ＋ Publish an event
                      </button>
                    </div>
                    <div className="filters">
                      <label>
                        Event type
                        <input
                          value={kind}
                          onChange={(e) => setKind(e.target.value)}
                          placeholder="All event types"
                        />
                      </label>
                      <label>
                        Status
                        <select
                          value={status}
                          onChange={(e) => setStatus(e.target.value)}
                        >
                          <option value="">All statuses</option>
                          <option value="pending">Pending</option>
                          <option value="completed">Completed</option>
                          <option value="completed_with_failures">
                            Completed with failures
                          </option>
                        </select>
                      </label>
                      <label>
                        From
                        <input
                          type="datetime-local"
                          value={from}
                          onChange={(e) => setFrom(e.target.value)}
                        />
                      </label>
                      <label>
                        To
                        <input
                          type="datetime-local"
                          value={to}
                          onChange={(e) => setTo(e.target.value)}
                        />
                      </label>
                    </div>
                    <div className="table-scroll">
                      <table>
                        <thead>
                          <tr>
                            <th>Event</th>
                            <th>Status</th>
                            <th>Accepted</th>
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {events.map((event) => (
                            <tr key={event.id}>
                              <td>
                                <strong>{event.type}</strong>
                                <span className="mono subtext">
                                  {short(event.id)}…
                                </span>
                              </td>
                              <td>
                                <Badge value={event.status} />
                              </td>
                              <td>{stamp(event.accepted_at)}</td>
                              <td>
                                <button
                                  aria-label={"Inspect event " + event.id}
                                  className="text-button"
                                  onClick={() => openEvent(event.id)}
                                >
                                  Inspect ↗
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {!events.length && (
                      <div className="empty compact">
                        <span className="empty-symbol">↗</span>
                        <h3>No events here yet</h3>
                        <p>
                          Published events and their delivery outcomes will
                          appear here.
                        </p>
                      </div>
                    )}
                    {next && (
                      <div className="panel-footer">
                        <button
                          disabled={busy}
                          onClick={() => action(() => load(next))}
                        >
                          Load more events
                        </button>
                      </div>
                    )}
                  </section>
                </>
              )}
              {tab === "Endpoints" && (
                <>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>{edit ? "Edit endpoint" : "Add an endpoint"}</h2>
                      {edit && (
                        <button onClick={() => setEdit(null)}>
                          Cancel edit
                        </button>
                      )}
                    </div>
                    <form
                      className="endpoint-form"
                      key={edit?.id || "new"}
                      onSubmit={(event) => {
                        const f = fields(event);
                        action(async () => {
                          const body = {
                            url: String(f.get("url")),
                            description: String(f.get("description")),
                          };
                          if (edit) {
                            await api(
                              `/v1/projects/${project}/endpoints/${edit.id}`,
                              "PATCH",
                              body,
                            );
                            setEdit(null);
                          } else {
                            const v = await api<{
                              signing_key: { secret: string };
                            }>(
                              `/v1/projects/${project}/endpoints`,
                              "POST",
                              body,
                            );
                            setSecret(v.signing_key.secret);
                          }
                          await load();
                          setNotice(
                            edit
                              ? "Endpoint updated. Existing events retain their original URL version."
                              : "Endpoint created. Save the signing secret before publishing.",
                          );
                        });
                      }}
                    >
                      <label>
                        Destination URL
                        <input
                          type="url"
                          name="url"
                          placeholder="https://api.customer.com/webhooks"
                          maxLength={2048}
                          defaultValue={edit?.url}
                          required
                        />
                      </label>
                      <label>
                        Description
                        <input
                          name="description"
                          maxLength={500}
                          defaultValue={edit?.description}
                          placeholder="Customer notifications"
                        />
                      </label>
                      <button
                        className="primary"
                        disabled={busy || !!currentProject?.archived_at}
                      >
                        {edit ? "Save endpoint" : "Create endpoint"}
                      </button>
                    </form>
                    <p className="hint">
                      Public HTTPS destinations on port 443. Local receivers
                      require the explicit development allowlist.
                    </p>
                  </section>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>
                        Destinations{" "}
                        <span className="count">{endpoints.length}</span>
                      </h2>
                    </div>
                    {endpoints.length ? (
                      endpoints.map((endpoint) => (
                        <div className="endpoint-row" key={endpoint.id}>
                          <span
                            className={
                              "endpoint-indicator " +
                              (endpoint.paused ? "paused" : "")
                            }
                          >
                            ◎
                          </span>
                          <div className="endpoint-info">
                            <strong>
                              {endpoint.description || "Unnamed endpoint"}
                            </strong>
                            <p>{endpoint.url}</p>
                            <small className="mono">
                              {endpoint.id} · v{endpoint.version}
                            </small>
                          </div>
                          <Badge
                            value={endpoint.paused ? "paused" : "active"}
                          />
                          <div className="button-row">
                            <button
                              disabled={busy || !!currentProject?.archived_at}
                              onClick={() =>
                                action(async () => {
                                  await api(
                                    `/v1/projects/${project}/endpoints/${endpoint.id}`,
                                    "PATCH",
                                    { paused: !endpoint.paused },
                                  );
                                  await load();
                                })
                              }
                            >
                              {endpoint.paused ? "Resume" : "Pause"}
                            </button>
                            <button onClick={() => setEdit(endpoint)}>
                              Edit
                            </button>
                            <button
                              disabled={busy || !!currentProject?.archived_at}
                              onClick={() =>
                                action(async () => {
                                  const v = await api<{ secret: string }>(
                                    `/v1/projects/${project}/endpoints/${endpoint.id}/rotate-secret`,
                                    "POST",
                                    {},
                                  );
                                  setSecret(v.secret);
                                  setNotice(
                                    "New signing key activates in ten minutes. Update the receiver first.",
                                  );
                                })
                              }
                            >
                              Rotate secret
                            </button>
                            <button
                              className="danger-text"
                              disabled={busy || !!currentProject?.archived_at}
                              onClick={() => {
                                if (
                                  window.confirm(
                                    "Immediately revoke all signing keys for this endpoint? Update your receiver with the replacement secret.",
                                  )
                                )
                                  action(async () => {
                                    const v = await api<{ secret: string }>(
                                      `/v1/projects/${project}/endpoints/${endpoint.id}/rotate-secret`,
                                      "POST",
                                      { emergency: true },
                                    );
                                    setSecret(v.secret);
                                    setNotice(
                                      "Previous keys revoked. The replacement is active now.",
                                    );
                                  });
                              }}
                            >
                              Emergency rotation
                            </button>
                          </div>
                        </div>
                      ))
                    ) : (
                      <div className="empty compact">
                        <h3>No destinations configured</h3>
                        <p>Add an endpoint to start delivering events.</p>
                      </div>
                    )}
                  </section>
                </>
              )}
              {tab === "API keys" && (
                <>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>Create ingestion key</h2>
                    </div>
                    <form
                      className="inline-form"
                      onSubmit={(event) => {
                        const f = fields(event);
                        action(async () => {
                          const expires = f.get("expires");
                          const v = await api<{ token: string }>(
                            `/v1/projects/${project}/keys`,
                            "POST",
                            {
                              name: f.get("name"),
                              ...(expires
                                ? {
                                    expires_at: new Date(
                                      String(expires),
                                    ).toISOString(),
                                  }
                                : {}),
                            },
                          );
                          setSecret(v.token);
                          await load();
                        });
                      }}
                    >
                      <label>
                        Key name
                        <input
                          name="name"
                          required
                          maxLength={120}
                          placeholder="Production publisher"
                        />
                      </label>
                      <label>
                        Expiry (optional)
                        <input name="expires" type="datetime-local" />
                      </label>
                      <button
                        disabled={busy || !!currentProject?.archived_at}
                        className="primary"
                      >
                        Generate key
                      </button>
                    </form>
                    <p className="hint">
                      Keys can publish events to this project. They cannot read
                      dashboard data. Tokens are shown once.
                    </p>
                  </section>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>Project keys</h2>
                    </div>
                    <div className="table-scroll">
                      <table>
                        <thead>
                          <tr>
                            <th>Name</th>
                            <th>Prefix</th>
                            <th>Scope</th>
                            <th>Status</th>
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {keys.map((key) => (
                            <tr key={key.id}>
                              <td>{key.name}</td>
                              <td className="mono">{key.prefix}…</td>
                              <td>
                                <code>events:write</code>
                              </td>
                              <td>
                                <Badge
                                  value={
                                    key.revoked_at
                                      ? "revoked"
                                      : key.expires_at &&
                                          new Date(key.expires_at) < new Date()
                                        ? "expired"
                                        : "active"
                                  }
                                />
                              </td>
                              <td>
                                <button
                                  className="danger-text"
                                  disabled={busy || !!key.revoked_at}
                                  onClick={() => {
                                    if (
                                      window.confirm(
                                        "Revoke this ingestion key? Existing accepted deliveries will continue.",
                                      )
                                    )
                                      action(async () => {
                                        await api(
                                          `/v1/projects/${project}/keys/${key.id}`,
                                          "DELETE",
                                        );
                                        await load();
                                      });
                                  }}
                                >
                                  Revoke
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {!keys.length && (
                      <div className="empty compact">
                        <h3>No ingestion keys yet</h3>
                        <p>Generate a key for your publishing application.</p>
                      </div>
                    )}
                  </section>
                </>
              )}
              {tab === "Project" && (
                <>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>Project details</h2>
                    </div>
                    <form
                      className="inline-form"
                      onSubmit={(event) => {
                        const f = fields(event);
                        action(async () => {
                          await api(`/v1/projects/${project}`, "PATCH", {
                            name: f.get("name"),
                          });
                          await loadProjects();
                          setNotice("Project renamed.");
                        });
                      }}
                    >
                      <label>
                        Project name
                        <input
                          name="name"
                          defaultValue={currentProject?.name}
                          key={project}
                          maxLength={120}
                          required
                        />
                      </label>
                      <button disabled={busy} className="primary">
                        Save name
                      </button>
                    </form>
                  </section>
                  <section className="panel danger-panel">
                    <h2>Archive project</h2>
                    <p>
                      Stops new event acceptance and cancels unstarted
                      deliveries. Requests already in flight cannot be recalled.
                      History remains available.
                    </p>
                    <button
                      disabled={busy || !!currentProject?.archived_at}
                      className="danger-text"
                      onClick={() => {
                        if (
                          window.confirm(
                            "Archive this project and cancel its unstarted deliveries? This cannot be undone.",
                          )
                        )
                          action(async () => {
                            await api(`/v1/projects/${project}`, "PATCH", {
                              archived: true,
                            });
                            await refresh();
                            setNotice("Project archived.");
                          });
                      }}
                    >
                      {currentProject?.archived_at
                        ? "Project archived"
                        : "Archive project"}
                    </button>
                  </section>
                </>
              )}
            </>
          )}
          <footer>
            <span>
              <Shield /> Hookgarde
            </span>
            <p>
              At-least-once delivery · finite retries · inspectable recovery
            </p>
            <small>Self-hosted alpha</small>
          </footer>
        </main>
      </div>
      {publish && (
        <div className="modal-backdrop">
          <section
            ref={modalRef}
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="publish-title"
          >
            <div className="panel-heading">
              <div>
                <p className="eyebrow">DEVELOPER TOOL</p>
                <h2 id="publish-title">Publish an event</h2>
              </div>
              <button
                onClick={() => setPublish(false)}
                aria-label="Close publish dialog"
              >
                ×
              </button>
            </div>
            <p className="muted">
              Uses your ingestion key. The owner session cannot publish events.
            </p>
            <form
              onSubmit={(event) => {
                const f = fields(event);
                action(async () => {
                  const data = JSON.parse(String(f.get("data")));
                  const token = String(f.get("token"));
                  const result = await api(
                    `/v1/projects/${project}/events`,
                    "POST",
                    {
                      type: f.get("type"),
                      data,
                      endpoint_ids: f.getAll("endpoint_ids"),
                    },
                    {
                      Authorization: "Bearer " + token,
                      "Idempotency-Key": String(f.get("idempotency")),
                    },
                  );
                  setReceipt(result);
                  await load();
                  setNotice(
                    "Event accepted durably. Delivery runs asynchronously.",
                  );
                });
              }}
            >
              <label>
                Ingestion token
                <input
                  name="token"
                  type="password"
                  autoComplete="off"
                  required
                  placeholder="hg_…"
                />
              </label>
              <div className="form-columns">
                <label>
                  Event type
                  <input
                    name="type"
                    required
                    maxLength={128}
                    defaultValue="order.paid"
                  />
                </label>
                <label>
                  Idempotency key
                  <input
                    name="idempotency"
                    required
                    maxLength={128}
                    defaultValue={crypto.randomUUID()}
                  />
                </label>
              </div>
              <fieldset>
                <legend>Destinations</legend>
                {endpoints
                  .filter((e) => !e.paused)
                  .map((e) => (
                    <label className="checkbox" key={e.id}>
                      <input name="endpoint_ids" type="checkbox" value={e.id} />
                      {e.description || e.url}
                    </label>
                  ))}
              </fieldset>
              <label>
                Data (JSON object)
                <textarea
                  name="data"
                  required
                  defaultValue={'{\n  "order_id": "order_123"\n}'}
                />
              </label>
              <button disabled={busy} className="primary">
                Publish event
              </button>
            </form>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            {receipt != null && (
              <pre className="payload" role="status">
                {JSON.stringify(receipt, null, 2)}
              </pre>
            )}
            <p className="hint">
              Retry using the same key and exact body after an uncertain
              response. Reusing the key with changed bytes returns a conflict.
            </p>
          </section>
        </div>
      )}
    </div>
  );
}
