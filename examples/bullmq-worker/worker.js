const Redis = require('ioredis')
const { Pool } = require('pg')
const http = require('http')
const https = require('https')

const redis = new Redis(process.env.REDIS_URL || 'redis://localhost:6379')
const pool = new Pool({ connectionString: process.env.DATABASE_URL })

const QUEUE_KEY = 'jobs:notifications'
const notificationURL = process.env.NOTIFICATION_SERVICE_URL || 'http://notification-service:8080'
const billingURL = process.env.BILLING_SERVICE_URL || 'http://billing-service:8080'

async function handleSendNotification(job) {
  const { rows } = await pool.query('SELECT id, email, name FROM users WHERE id = $1', [job.user_id])
  if (rows.length === 0) throw new Error(`User ${job.user_id} not found`)
  const user = rows[0]

  await post(`${notificationURL}/api/notify`, {
    to: user.email,
    channel: job.channel,
    message: job.message,
    user_name: user.name,
  })

  await pool.query(
    'INSERT INTO notification_log (user_id, channel, message, sent_at) VALUES ($1, $2, $3, NOW())',
    [job.user_id, job.channel, job.message]
  )
}

async function handleGenerateInvoice(job) {
  const { rows } = await pool.query(
    'SELECT id, amount, product FROM orders WHERE id = $1 AND user_id = $2',
    [job.order_id, job.user_id]
  )
  if (rows.length === 0) throw new Error(`Order ${job.order_id} not found`)
  const order = rows[0]

  await pool.query(
    'INSERT INTO invoices (user_id, order_id, amount, issued_at) VALUES ($1, $2, $3, NOW())',
    [job.user_id, job.order_id, order.amount]
  )

  await post(`${billingURL}/api/invoice-created`, {
    event: 'invoice_created',
    user_id: job.user_id,
    order_id: job.order_id,
    amount: order.amount,
  })
}

async function processJob(raw) {
  const job = JSON.parse(raw)
  switch (job.type) {
    case 'send_notification':
      return handleSendNotification(job)
    case 'generate_invoice':
      return handleGenerateInvoice(job)
    default:
      throw new Error(`Unknown job type: ${job.type}`)
  }
}

function post(url, body) {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body)
    const parsed = new URL(url)
    const lib = parsed.protocol === 'https:' ? https : http
    const req = lib.request(
      { hostname: parsed.hostname, port: parsed.port, path: parsed.pathname, method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } },
      (res) => { res.resume(); res.on('end', resolve) }
    )
    req.on('error', reject)
    req.write(data)
    req.end()
  })
}

function startHealthServer() {
  const server = http.createServer((req, res) => {
    if (req.url === '/health') {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ status: 'healthy' }))
    } else {
      res.writeHead(404)
      res.end()
    }
  })
  server.listen(8080, () => console.log('Health server listening on :8080'))
}

async function waitForDB(retries = 10) {
  for (let i = 0; i < retries; i++) {
    try {
      await pool.query('SELECT 1')
      return
    } catch {
      await new Promise(r => setTimeout(r, 1000))
    }
  }
  throw new Error('Database not ready')
}

async function main() {
  startHealthServer()
  await waitForDB()
  console.log(`Worker started, polling ${QUEUE_KEY}`)
  while (true) {
    const result = await redis.brpop(QUEUE_KEY, 5)
    if (!result) continue
    try {
      await processJob(result[1])
    } catch (err) {
      console.error(`Job error: ${err.message}`)
    }
  }
}

main().catch(err => { console.error(err); process.exit(1) })
