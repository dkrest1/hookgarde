import { test, expect } from "@playwright/test";
// These are UI contract tests with mocked API responses. PostgreSQL correctness
// is covered by the Go integration suite, not by these browser fixtures.
test("owner can configure a project, save secrets, inspect and replay an event", async ({
  page,
}) => {
  const project = "11111111-1111-4111-8111-111111111111",
    endpoint = "22222222-2222-4222-8222-222222222222",
    event = "33333333-3333-4333-8333-333333333333",
    delivery = "44444444-4444-4444-8444-444444444444";
  let loggedIn = false;
  let projects: unknown[] = [],
    endpoints: unknown[] = [],
    keys: unknown[] = [],
    replays = 0;
  const eventRow = {
    id: event,
    type: "order.paid",
    accepted_at: "2026-11-01T12:00:00Z",
    status: "completed_with_failures",
  };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api", ""),
      method = request.method();
    let status = 200;
    let data: unknown = {};
    if (path === "/v1/auth/login") {
      loggedIn = true;
      data = {
        user: { email: "owner@example.com" },
        organization: { name: "Acme", id: "org" },
        role: "owner",
      };
    } else if (!loggedIn) {
      status = 401;
      data = { error: { message: "Login required" } };
    } else if (path === "/v1/me")
      data = {
        user: { email: "owner@example.com" },
        organization: { name: "Acme", id: "org" },
        role: "owner",
      };
    else if (path === "/v1/auth/csrf") data = { csrf_token: "test-csrf" };
    else if (path === "/v1/usage")
      data = {
        accepted_event_count: 1,
        limits: { events_per_month: 10000 },
        reset_at: "2026-12-01T00:00:00Z",
      };
    else if (path === "/v1/projects" && method === "POST") {
      status = 201;
      const body = request.postDataJSON();
      data = { id: project, name: body.name, archived_at: null };
      projects.push(data);
    } else if (path === "/v1/projects")
      data = { data: projects, next_cursor: null };
    else if (path.endsWith("/endpoints") && method === "POST") {
      status = 201;
      const body = request.postDataJSON();
      const ep = {
        id: endpoint,
        url: body.url,
        description: body.description,
        paused: false,
        version: 1,
      };
      endpoints.push(ep);
      data = {
        endpoint: ep,
        signing_key: { secret: "one-time-signing-secret" },
      };
    } else if (path.endsWith("/endpoints"))
      data = { data: endpoints, next_cursor: null };
    else if (path.endsWith("/keys") && method === "POST") {
      status = 201;
      keys.push({
        id: "key",
        name: "Publisher",
        prefix: "hg_prefix",
        revoked_at: null,
        expires_at: null,
      });
      data = { key: keys[0], token: "hg_one_time_token" };
    } else if (path.endsWith("/keys")) data = { data: keys, next_cursor: null };
    else if (path.endsWith("/events"))
      data = { data: [eventRow], next_cursor: null };
    else if (path.endsWith("/events/" + event))
      data = {
        event: eventRow,
        envelope: {
          id: event,
          data: { message: '<script>alert("xss")</script>' },
        },
        payload_purged_at: null,
        deliveries: [
          {
            id: delivery,
            endpoint_id: endpoint,
            generation: 1,
            state: "failed",
            attempts_count: 6,
            last_error: "receiver_http_503",
            endpoint_version: 1,
            deadline_at: "2026-11-02T12:00:00Z",
          },
        ],
      };
    else if (path.endsWith("/attempts"))
      data = {
        data: [
          {
            id: "attempt",
            sequence: 1,
            outcome: "unknown",
            started_at: "2026-11-01T12:00:00Z",
            http_status: null,
            duration_ms: null,
            error_class: "lease_expired_unknown",
            response_preview: "<img src=x onerror=alert(1)>",
          },
        ],
        next_cursor: null,
      };
    else if (path.endsWith("/replay")) {
      status = 202;
      replays++;
      data = { delivery_id: "new-delivery", idempotency_replayed: false };
    } else if (path === "/v1/auth/logout") {
      loggedIn = false;
      status = 204;
    }
    await route.fulfill({
      status,
      contentType: "application/json",
      body: status === 204 ? "" : JSON.stringify(data),
    });
  });
  await page.goto("/");
  await page.getByLabel("Email address").fill("owner@example.com");
  await page
    .getByLabel("Password", { exact: true })
    .fill("correct-horse-battery-staple");
  await page.getByRole("button", { name: "Sign in to Hookgarde" }).click();
  await expect(
    page.getByRole("heading", { name: "Delivery overview" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Create your first project" }).click();
  await page
    .getByLabel("Project name", { exact: true })
    .fill("Acme production");
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Endpoints", exact: false })
    .first()
    .click();
  await page.getByLabel("Destination URL").fill("https://example.com/webhook");
  await page
    .getByLabel("Description", { exact: true })
    .fill("Customer notifications");
  await page
    .getByRole("button", { name: "Create endpoint", exact: true })
    .click();
  await expect(
    page.getByText("one-time-signing-secret", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "I saved it" }).click();
  await expect(
    page.getByText("one-time-signing-secret", { exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "API keys", exact: false })
    .first()
    .click();
  await page.getByLabel("Key name").fill("Publisher");
  await page.getByRole("button", { name: "Generate key" }).click();
  await expect(
    page.getByText("hg_one_time_token", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "I saved it" }).click();
  await page.getByRole("button", { name: "Events", exact: true }).click();
  await page.getByRole("button", { name: "Inspect event " + event }).click();
  await expect(
    page.getByRole("heading", { name: "Event details" }),
  ).toBeVisible();
  await expect(page.locator(".payload")).toContainText("<script>");
  await expect(page.locator(".payload script")).toHaveCount(0);
  await page.getByRole("button", { name: "Inspect attempts" }).click();
  await expect(
    page.getByText("lease_expired_unknown", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".attempt img")).toHaveCount(0);
  await page.getByRole("button", { name: "Replay delivery" }).click();
  await expect.poll(() => replays).toBe(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Event details" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await page.screenshot({
    path: "test-results/console-desktop.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
});
test("login errors remain visible and tokens are not stored in local storage", async ({
  page,
}) => {
  await page.route("**/api/**", (route) =>
    route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: { message: "Invalid email or password" } }),
    }),
  );
  await page.goto("/");
  await page.getByLabel("Email address").fill("owner@example.com");
  await page.getByLabel("Password", { exact: true }).fill("incorrect-password");
  await page.getByRole("button", { name: "Sign in to Hookgarde" }).click();
  await expect(page.getByRole("alert")).toHaveText("Invalid email or password");
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
});
