# Kafka configuration
KAFKA_BROKERS = ENV.fetch("KAFKA_BROKERS", "kafka:9092").split(",")
KAFKA_TOPIC = ENV.fetch("KAFKA_TOPIC", "todo-events")

# Write to stdout for debugging
puts "[KAFKA] Initializing Kafka with brokers: #{KAFKA_BROKERS.inspect}"
puts "[KAFKA] KAFKA_BROKERS env: #{ENV['KAFKA_BROKERS'].inspect}"

begin
  # Initialize Kafka client
  $kafka = Kafka.new(
    seed_brokers: KAFKA_BROKERS,
    client_id: "todo-api",
    logger: Rails.logger,
    connect_timeout: 30,
    socket_timeout: 30
  )

  puts "[KAFKA] Kafka client initialized successfully"
  Rails.logger.info "Kafka client initialized successfully"
rescue StandardError => e
  puts "[KAFKA] Failed to initialize Kafka: #{e.message}"
  puts "[KAFKA] Error: #{e.backtrace.first(5).join("\n")}"
  Rails.logger.error "Failed to initialize Kafka: #{e.message}"
  $kafka = nil
end
