import { Component, ComponentChildren, ErrorInfo as PreactErrorInfo } from "preact";

interface ErrorBoundaryProps {
  children?: ComponentChildren;
  fallback?: ComponentChildren;
  onError?: (error: Error, errorInfo: PreactErrorInfo) => void;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

/**
 * ErrorBoundary component that catches JavaScript errors in child components.
 * Displays a user-friendly fallback UI when an error occurs.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = {
      hasError: false,
      error: null,
    };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    // Update state so the next render shows the fallback UI
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: PreactErrorInfo): void {
    // Log error to error reporting service (if onError callback provided)
    if (this.props.onError) {
      this.props.onError(error, errorInfo);
    }
  }

  handleRetry = (): void => {
    this.setState({ hasError: false, error: null });
  };

  handleReloadPage = (): void => {
    window.location.reload();
  };

  render(): ComponentChildren {
    if (this.state.hasError) {
      // Use custom fallback if provided, otherwise show default error UI
      if (this.props.fallback) {
        return this.props.fallback;
      }

      return (
        <div style={styles.container}>
          <div style={styles.errorCard}>
            <div style={styles.iconContainer}>
              <span style={styles.icon}>⚠️</span>
            </div>
            <h2 style={styles.heading}>Something went wrong</h2>
            <p style={styles.message}>
              {this.state.error?.message || "An unexpected error occurred"}
            </p>
            <div style={styles.buttonContainer}>
              <button
                onClick={this.handleRetry}
                style={styles.retryButton}
                onMouseOver={(e) => (e.currentTarget.style.backgroundColor = "#dc2626")}
                onMouseOut={(e) => (e.currentTarget.style.backgroundColor = "#ef4444")}
              >
                Retry
              </button>
              <button
                onClick={this.handleReloadPage}
                style={styles.reloadButton}
                onMouseOver={(e) => (e.currentTarget.style.backgroundColor = "#374151")}
                onMouseOut={(e) => (e.currentTarget.style.backgroundColor = "#4b5563")}
              >
                Reload Page
              </button>
            </div>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}

const styles = {
  container: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    minHeight: "200px",
    padding: "2rem",
    backgroundColor: "#fef2f2",
    borderRadius: "8px",
    border: "1px solid #fecaca",
  },
  errorCard: {
    textAlign: "center" as const,
    maxWidth: "400px",
  },
  iconContainer: {
    marginBottom: "1rem",
  },
  icon: {
    fontSize: "3rem",
  },
  heading: {
    fontSize: "1.5rem",
    fontWeight: 600,
    color: "#991b1b",
    marginBottom: "0.5rem",
    marginTop: 0,
  },
  message: {
    fontSize: "0.875rem",
    color: "#7f1d1d",
    marginBottom: "1.5rem",
    lineHeight: 1.5,
  },
  buttonContainer: {
    display: "flex",
    gap: "0.75rem",
    justifyContent: "center",
  },
  retryButton: {
    padding: "0.5rem 1.25rem",
    fontSize: "0.875rem",
    fontWeight: 500,
    color: "#ffffff",
    backgroundColor: "#ef4444",
    border: "none",
    borderRadius: "6px",
    cursor: "pointer",
    transition: "background-color 0.15s ease",
  },
  reloadButton: {
    padding: "0.5rem 1.25rem",
    fontSize: "0.875rem",
    fontWeight: 500,
    color: "#ffffff",
    backgroundColor: "#4b5563",
    border: "none",
    borderRadius: "6px",
    cursor: "pointer",
    transition: "background-color 0.15s ease",
  },
};
