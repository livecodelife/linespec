class EventPublisher
  def self.publish_todo_created(todo)
    return unless $kafka

    Rails.logger.info "Publishing todo_created event for todo #{todo.id}"

    event = {
      event_type: "todo_created",
      todo_id: todo.id,
      title: todo.title,
      description: todo.description,
      completed: todo.completed,
      user_id: todo.user_id,
      created_at: todo.created_at.iso8601,
      updated_at: todo.updated_at.iso8601
    }

    begin
      $kafka.deliver_message(
        event.to_json,
        topic: KAFKA_TOPIC,
        key: todo.id.to_s
      )
      Rails.logger.info "Successfully published todo_created event"
    rescue StandardError => e
      Rails.logger.error "Failed to publish todo_created event: #{e.message}"
    end
  end

  def self.publish_todo_updated(todo)
    return unless $kafka

    Rails.logger.info "Publishing todo_updated event for todo #{todo.id}"

    event = {
      event_type: "todo_updated",
      todo_id: todo.id,
      title: todo.title,
      description: todo.description,
      completed: todo.completed,
      user_id: todo.user_id,
      created_at: todo.created_at.iso8601,
      updated_at: todo.updated_at.iso8601
    }

    begin
      $kafka.deliver_message(
        event.to_json,
        topic: KAFKA_TOPIC,
        key: todo.id.to_s
      )
      Rails.logger.info "Successfully published todo_updated event"
    rescue StandardError => e
      Rails.logger.error "Failed to publish todo_updated event: #{e.message}"
    end
  end

  def self.publish_todo_deleted(todo)
    return unless $kafka

    Rails.logger.info "Publishing todo_deleted event for todo #{todo.id}"

    event = {
      event_type: "todo_deleted",
      todo_id: todo.id,
      user_id: todo.user_id,
      deleted_at: Time.current.iso8601
    }

    begin
      $kafka.deliver_message(
        event.to_json,
        topic: KAFKA_TOPIC,
        key: todo.id.to_s
      )
      Rails.logger.info "Successfully published todo_deleted event"
    rescue StandardError => e
      Rails.logger.error "Failed to publish todo_deleted event: #{e.message}"
    end
  end
end
