import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  waitFor,
  fireEvent,
} from "@testing-library/react";
import Console from "../app/page";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
test("owner signs in, creates project and saves one-time endpoint secret", async () => {
  let loggedIn = false;
  const project = "11111111-1111-4111-8111-111111111111";
  let projects: unknown[] = [],
    endpoints: unknown[] = [];
  const requests: {
    url: string;
    method: string;
    headers: Record<string, string>;
  }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options: RequestInit = {}) => {
      const path = url.replace("/api", "").split("?")[0];
      const method = options.method || "GET";
      requests.push({
        url,
        method,
        headers: options.headers as Record<string, string>,
      });
      let status = 200,
        data: unknown = {};
      if (path === "/v1/auth/login") {
        loggedIn = true;
        data = {
          user: { email: "owner@example.com" },
          organization: { name: "Acme" },
          role: "owner",
        };
      } else if (!loggedIn) {
        status = 401;
        data = { error: { message: "Login required" } };
      } else if (path === "/v1/auth/csrf") data = { csrf_token: "csrf-test" };
      else if (path === "/v1/projects" && method === "POST") {
        data = { id: project, name: "Orders", archived_at: null };
        projects.push(data);
        status = 201;
      } else if (path === "/v1/projects")
        data = { data: projects, next_cursor: null };
      else if (path.endsWith("/endpoints") && method === "POST") {
        const ep = {
          id: "endpoint",
          url: "https://example.com",
          description: "Receiver",
          paused: false,
          version: 1,
        };
        endpoints.push(ep);
        data = { endpoint: ep, signing_key: { secret: "one-time-secret" } };
        status = 201;
      } else if (path.endsWith("/endpoints"))
        data = { data: endpoints, next_cursor: null };
      else if (path.endsWith("/events") || path.endsWith("/keys"))
        data = { data: [], next_cursor: null };
      else if (path === "/v1/usage")
        data = {
          accepted_event_count: 0,
          limits: { events_per_month: 10000 },
          reset_at: "2026-12-01",
        };
      return { ok: status < 400, status, json: async () => data };
    }),
  );
  render(<Console />);
  await screen.findByRole("heading", { name: "Welcome back" });
  fireEvent.change(screen.getByLabelText("Email address"), {
    target: { value: "owner@example.com" },
  });
  fireEvent.change(screen.getByLabelText("Password", { exact: true }), {
    target: { value: "correct-horse-battery-staple" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Sign in to Hookgarde/ }));
  await screen.findByRole("heading", { name: "Delivery overview" });
  fireEvent.click(
    screen.getByRole("button", { name: "Create your first project" }),
  );
  fireEvent.change(screen.getByLabelText("Project name", { exact: true }), {
    target: { value: "Orders" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create project" }));
  await waitFor(() =>
    expect(
      screen.getByRole("combobox", { name: "Project" }).getAttribute("value") ||
        (screen.getByRole("combobox", { name: "Project" }) as HTMLSelectElement)
          .value,
    ).toBe(project),
  );
  fireEvent.click(screen.getByRole("button", { name: /Endpoints/ }));
  await screen.findByRole("heading", { name: "Add an endpoint" });
  fireEvent.change(screen.getByLabelText("Destination URL"), {
    target: { value: "https://example.com" },
  });
  fireEvent.change(screen.getByLabelText("Description", { exact: true }), {
    target: { value: "Receiver" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create endpoint" }));
  await screen.findByText("one-time-secret", { exact: true });
  fireEvent.click(screen.getByRole("button", { name: /I saved it/ }));
  expect(screen.queryByText("one-time-secret", { exact: true })).toBeNull();
  expect(
    requests.find((r) => r.method === "POST" && r.url.includes("/endpoints"))
      ?.headers["X-CSRF-Token"],
  ).toBe("csrf-test");
  expect(localStorage.length).toBe(0);
});
test("generic login failure is visible", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: false,
      status: 401,
      json: async () => ({ error: { message: "Invalid email or password" } }),
    })),
  );
  render(<Console />);
  await screen.findByRole("heading", { name: "Welcome back" });
  fireEvent.change(screen.getByLabelText("Email address"), {
    target: { value: "owner@example.com" },
  });
  fireEvent.change(screen.getByLabelText("Password", { exact: true }), {
    target: { value: "incorrect-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Sign in to Hookgarde/ }));
  await waitFor(() =>
    expect(screen.getByRole("alert").textContent).toBe(
      "Invalid email or password",
    ),
  );
});
