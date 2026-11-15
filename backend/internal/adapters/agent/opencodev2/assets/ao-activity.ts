// agent-orchestrator: managed opencode-v2 activity plugin (do not edit)
//
// Child (subagent) sessions share this event bus and carry their own ids. They
// are tracked from session.created(parentID) and never reported as the session
// identity: AO persists the reported id as the native restore handle, so a
// child id would make restoration resume the subagent conversation. Child tool /
// execution activity is folded onto the root session as plain `active` so
// subagent work still reads as activity, while child turn boundaries are
// dropped — a child finishing does not end the parent's turn.
import { spawn } from "node:child_process"
import { createHash, randomBytes, timingSafeEqual } from "node:crypto"
import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import { dirname } from "node:path"

const HOOK_TIMEOUT_MS = 1_250
const REPORT_MAX_FRAME = 4 * 1024 * 1024

// The bridge belongs to the existing terminal's private server. Native prompt
// admission, rather than a pre-admission hook or a PTY write, supplies the ack.
async function startReportBridge(context, currentSessionID) {
  const descriptorPath = process.env.AO_OPENCODE2_REPORT_BRIDGE
  const address = process.env.AO_OPENCODE2_REPORT_SOCKET
  const sessionID = process.env.AO_SESSION_ID
  const launchID = process.env.AO_RUNTIME_LAUNCH_ID
  if (!descriptorPath || !address || !sessionID || !launchID) return async () => {}

  const token = randomBytes(32).toString("hex")
  const clients = new Set()
  let closing = false
  const server = createServer((socket) => {
    clients.add(socket)
    const abort = new AbortController()
    socket.on("close", () => { clients.delete(socket); abort.abort() })
    socket.on("error", () => {})
    socket.setTimeout(12_000, () => socket.destroy())
    let input = Buffer.alloc(0)
    let handled = false
    socket.on("data", (chunk) => {
      if (handled) return
      if (input.length + chunk.length > REPORT_MAX_FRAME) { socket.destroy(); return }
      input = Buffer.concat([input, chunk])
      const end = input.indexOf(10)
      if (end < 0) return
      handled = true
      void admit(input.subarray(0, end), abort.signal).then(
        (reply) => { if (!socket.destroyed) socket.end(JSON.stringify(reply) + "\n") },
        (error) => {
          if (!socket.destroyed) socket.end(JSON.stringify({ version: 1, error: String(error?.message ?? error).slice(0, 500) }) + "\n")
        },
      )
    })
  })
  server.on("error", () => {})

  async function admit(frame, signal) {
    const request = JSON.parse(frame.toString("utf8"))
    const provided = Buffer.from(typeof request.token === "string" ? request.token : "")
    const expected = Buffer.from(token)
    if (provided.length !== expected.length || !timingSafeEqual(provided, expected)) throw new Error("Report bridge authentication failed")
    if (closing || request.version !== 1 || request.sessionID !== sessionID || request.launchID !== launchID) {
      throw new Error("Report bridge controller generation changed")
    }
    const nativeID = currentSessionID()
    if (!nativeID || request.nativeSessionID !== nativeID) throw new Error("Report target is not the current root conversation")
    if (typeof request.deliveryID !== "string" || !request.deliveryID || typeof request.text !== "string" || !request.text.trim()) {
      throw new Error("Report requires a delivery identity and content")
    }
    const messageID = "msg_" + createHash("sha256").update(nativeID + "\0" + request.deliveryID).digest("hex")
    if (request.messageID !== messageID) throw new Error("Report message identity does not match its batch")
    const version = /^v?2\.(\d+)\.(\d+)(?:[-+].*)?$/.exec(String(context.app?.version ?? ""))
    if (!version || Number(version[1]) < 3 || typeof context.session?.prompt !== "function") {
      throw new Error("OpenCode 2.3.0 or newer is required for durable terminal reports")
    }
    const receipt = await context.session.prompt({
      sessionID: nativeID,
      id: messageID,
      text: request.text,
      delivery: "queue",
      metadata: { ao: { kind: "worker_report", deliveryID: request.deliveryID } },
    }, { signal })
    if (receipt?.id !== messageID || receipt?.sessionID !== nativeID || receipt?.type !== "user" || receipt?.delivery !== "queue") {
      throw new Error("OpenCode returned no matching durable report admission")
    }
    return {
      version: 1, accepted: true, sessionID, launchID, nativeSessionID: nativeID,
      deliveryID: request.deliveryID, messageID,
    }
  }

  async function close() {
    closing = true
    for (const socket of clients) socket.destroy()
    // An old plugin must not remove the descriptor published by its replacement.
    // Keep the address bound until this check finishes: a replacement publishes
    // only after binding, so it cannot race this owner's descriptor removal.
    try {
      const published = JSON.parse(await readFile(descriptorPath, "utf8"))
      if (published.token === token) await unlink(descriptorPath)
    } catch {}
    if (server.listening) await new Promise((resolve) => server.close(resolve))
  }

  let temporary
  try {
    await mkdir(dirname(descriptorPath), { recursive: true, mode: 0o700 })
    if (process.platform !== "win32") await mkdir(dirname(address), { recursive: true, mode: 0o700 })
    await new Promise((resolve, reject) => {
      server.once("error", reject)
      server.listen(address, () => { server.removeListener("error", reject); resolve() })
    })
    server.unref()
    temporary = descriptorPath + ".tmp-" + randomBytes(8).toString("hex")
    await writeFile(temporary, JSON.stringify({ version: 1, sessionID, launchID, address, token }) + "\n", { mode: 0o600, flag: "wx" })
    await rename(temporary, descriptorPath)
    return close
  } catch (error) {
    await close()
    if (temporary) await unlink(temporary).catch(() => {})
    console.error("[Orchestrator] Terminal report bridge unavailable:", String(error?.message ?? error))
    return async () => {}
  }
}

export default {
  id: "agent-orchestrator.activity.v2",
  async setup(context) {
    const launchID = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
    const seenSessions = new Set()
    const childSessions = new Set()
    let rootSessionID = null
    const controller = new AbortController()

    let queue = Promise.resolve()

    function send(event, sessionID, payload) {
      return new Promise((resolve) => {
        try {
          const child = spawn("ao", ["hooks", "opencode-v2", event], {
            cwd: context.location.directory,
            env: { ...process.env, AO_RUNTIME_LAUNCH_ID: launchID },
            stdio: ["pipe", "ignore", "ignore"],
          })
          const timer = setTimeout(() => child.kill(), HOOK_TIMEOUT_MS)
          const done = () => {
            clearTimeout(timer)
            resolve()
          }
          child.on("error", done)
          child.on("close", done)
          child.stdin.on("error", () => {})
          child.stdin.end(JSON.stringify({ ...payload, session_id: sessionID, launch_id: launchID }) + "\n")
        } catch {
          resolve()
        }
      })
    }

    // Activity is observational: report in order but asynchronously so it never blocks OpenCode.
    function report(event, sessionID, payload = {}) {
      if (!sessionID) return
      queue = queue.then(() => send(event, sessionID, payload))
    }

    function ensureSession(sessionID) {
      if (!sessionID || seenSessions.has(sessionID)) return
      seenSessions.add(sessionID)
      report("session-start", sessionID)
    }

    function isChildSession(sessionID) {
      return !!sessionID && childSessions.has(sessionID)
    }

    // Fold a session id onto the root session AO may see. Children resolve to
    // the current root (null until one is known) and are never forwarded; a
    // non-child id becomes the current root, preserving OpenCode's supported
    // new-native-session flows and AO's checkpoint reset on identity change.
    function resolveSessionID(sessionID) {
      if (!sessionID) return null
      if (childSessions.has(sessionID)) return rootSessionID
      rootSessionID = sessionID
      return sessionID
    }

    function handleEvent(event) {
      const data = event?.data
      const sessionID = data?.sessionID
      switch (event?.type) {
        case "session.created":
          if (!sessionID) break
          // Nested subagents carry their immediate parent's id; any session
          // with a parent is a child and must never become the identity.
          if (data?.parentID) {
            childSessions.add(sessionID)
            break
          }
          rootSessionID = sessionID
          ensureSession(sessionID)
          break
        case "session.execution.started": {
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("active", root)
          break
        }
        case "session.execution.succeeded":
        case "session.execution.failed":
        case "session.execution.interrupted":
          // A child's execution boundary says nothing about the parent turn.
          if (!sessionID || isChildSession(sessionID)) break
          rootSessionID = sessionID
          ensureSession(sessionID)
          report("stop", sessionID)
          break
        case "session.status":
          if (!sessionID || isChildSession(sessionID)) break
          rootSessionID = sessionID
          ensureSession(sessionID)
          if (data?.status?.type === "idle") report("stop", sessionID)
          else if (data?.status?.type === "busy") report("active", sessionID)
          break
        case "permission.asked": {
          // A child dialog still blocks the tree; surface it on the root id.
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("permission-blocked", root, { permission_id: data?.id ?? "" })
          break
        }
        case "permission.replied": {
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("permission-resolved", root, { permission_id: data?.requestID ?? "", reply: data?.reply ?? "" })
          break
        }
      }
    }

    const eventLoop = (async () => {
      try {
        for await (const event of context.event.subscribe({ signal: controller.signal })) handleEvent(event)
      } catch {
        // Event delivery and AO reporting are both best effort.
      }
    })()

    const registrations = await Promise.all([
      context.tool.hook("execute.before", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) report("active", root)
        else report("active", root, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
      context.tool.hook("execute.after", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) report("active", root)
        else report("active", root, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
    ])

    const stopReportBridge = await startReportBridge(context, () => rootSessionID)

    return async () => {
      await stopReportBridge()
      controller.abort()
      await queue
      await Promise.allSettled(registrations.map((registration) => registration.dispose()))
      await eventLoop
    }
  },
}
