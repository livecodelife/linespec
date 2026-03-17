// import type { Plugin } from "@opencode-ai/plugin"
// import { tool } from "@opencode-ai/plugin"
// import type { EventSessionCreated, EventSessionIdle, EventFileEdited } from "@opencode-ai/sdk"

// export const LineSpecPlugin: Plugin = async ({ $, directory }) => {
//   return {
//     event: async ({ event }) => {
//       try {
//         if (event.type === "session.created") {
//           await $`cd ${directory} && ./linespec provenance search --query "architectural decisions and changes" --limit 10`
//         } else if (event.type === "session.idle") {
//           await $`cd ${directory} && ./linespec provenance audit --description "Review session changes for consistency with provenance history"`
//         } else if (event.type === "file.edited") {
//           const fileEvent = event as EventFileEdited
//           await $`cd ${directory} && ./linespec provenance context --path "${fileEvent.properties.file}"`
//         }
//       } catch {
//         // Silently ignore errors - provenance may not be initialized
//       }
//     },
//     tool: {
//       provenance_context: tool({
//         description: "Show provenance context for files - check constraints before editing",
//         args: {},
//         async execute(_args, context) {
//           const { directory } = context
//           try {
//             const result = await $`cd ${directory} && ./linespec provenance context`
//             return result.stdout || "No provenance context found"
//           } catch (e) {
//             return `Error getting provenance context: ${e}`
//           }
//         },
//       }),
//       provenance_search: tool({
//         description: "Search provenance records by semantic similarity",
//         args: {
//           query: tool.schema.string(),
//           limit: tool.schema.number().optional(),
//         },
//         async execute(args, context) {
//           const { directory } = context
//           try {
//             const limitArg = args.limit ? `--limit ${args.limit}` : ""
//             const result = await $`cd ${directory} && ./linespec provenance search --query "${args.query}" ${limitArg}`
//             return result.stdout || "No results found"
//           } catch (e) {
//             return `Error searching provenance: ${e}`
//           }
//         },
//       }),
//       provenance_audit: tool({
//         description: "Audit changes against provenance history",
//         args: {
//           description: tool.schema.string().optional(),
//         },
//         async execute(args, context) {
//           const { directory } = context
//           try {
//             const descArg = args.description ? `--description "${args.description}"` : ""
//             const result = await $`cd ${directory} && ./linespec provenance audit ${descArg}`
//             return result.stdout || "Audit complete"
//           } catch (e) {
//             return `Error running audit: ${e}`
//           }
//         },
//       }),
//     },
//   }
// }