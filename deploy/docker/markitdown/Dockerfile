# Markitdown conversion sidecar for RAG binary document ingest.
# Converts PDF/DOCX/PPTX/XLSX uploads to markdown before chunk + embed.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

# Pinned EXACT per the adopted design. [all] pulls every format converter;
# no Azure Document Intelligence key is ever configured in this container,
# so the Azure path stays dormant and nothing calls out to Azure.
RUN pip install --no-cache-dir "markitdown[all]==0.1.7"

COPY deploy/docker/markitdown/app.py /app/app.py
COPY deploy/docker/markitdown/test_convert.py /app/test_convert.py
COPY deploy/docker/markitdown/testdata/ /app/testdata/

# Run unprivileged. The app writes only to /tmp for its conversion temp files.
RUN useradd --system --uid 10001 markitdown && chown -R markitdown /app
USER markitdown

EXPOSE 8700

CMD ["python", "/app/app.py"]
