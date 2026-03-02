class ApplicationController < ActionController::API
  before_action :authenticate_user, except: [ :login, :create ]

  attr_reader :current_user

  private

  def authenticate_user
    token = extract_token_from_header || extract_token_from_params
    Rails.logger.debug "AUTH DEBUG - Header token: #{extract_token_from_header.inspect}, Params token: #{extract_token_from_params.inspect}"
    return render_error("Unauthorized", "Invalid or expired token", 401) if token.nil?

    @current_user = User.find_by(token: token)
    render_error("Unauthorized", "Invalid or expired token", 401) if @current_user.nil?
  end

  def extract_token_from_header
    header = request.headers["Authorization"]
    return nil if header.nil?

    header.split(" ").last if header.start_with?("Bearer ")
  end

  def extract_token_from_params
    auth = params[:authorization]
    return nil if auth.nil?

    auth.gsub("Bearer ", "") if auth.start_with?("Bearer ")
  end

  def render_error(error, message, status)
    render json: { error: error, message: message }, status: status
  end
end
