# syntax = docker/dockerfile:1

ARG RUBY_VERSION=3.2.2
FROM docker.io/library/ruby:$RUBY_VERSION-slim

# Install base packages
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y curl libjemalloc2 libmariadb-dev build-essential pkg-config && \
    rm -rf /var/lib/apt/lists /var/cache/apt/archives

WORKDIR /app

# Set environment
ENV RAILS_ENV="development"

COPY Gemfile Gemfile.lock ./
RUN bundle install

COPY . .

EXPOSE 3000

CMD ["bash", "-c", "rm -f tmp/pids/server.pid && bundle exec rails db:migrate && bundle exec rails server -b 0.0.0.0"]
