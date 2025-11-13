import axios, { AxiosError } from 'axios';
import type { AxiosInstance } from 'axios';
import type { ApiResponse, ApiError, ConfigResponse, CalibrationStatus } from '../types/api';

// API configuration
const API_CONFIG = {
  baseURL: '/api', // Proxied through Vite dev server
  timeout: 10000,
  retryAttempts: 3,
  retryDelay: 1000,
};

class ApiClient {
  private client: AxiosInstance;

  constructor() {
    this.client = axios.create({
      baseURL: API_CONFIG.baseURL,
      timeout: API_CONFIG.timeout,
      headers: {
        'Content-Type': 'application/json',
      },
    });

    // Add response interceptor for error handling
    this.client.interceptors.response.use(
      (response) => response,
      (error: AxiosError) => this.handleError(error)
    );
  }

  private async handleError(error: AxiosError): Promise<never> {
    const responseData = error.response?.data as any;
    const apiError: ApiError = {
      message: responseData?.message || error.message || 'An unexpected error occurred',
      status: error.response?.status || 500,
    };

    console.error('[API Error]', apiError);
    throw apiError;
  }

  private async retryRequest<T>(
    requestFn: () => Promise<T>,
    retries: number = API_CONFIG.retryAttempts
  ): Promise<T> {
    try {
      return await requestFn();
    } catch (error) {
      if (retries > 0 && this.isRetryableError(error as ApiError)) {
        console.log(`[API] Retrying request... (${retries} attempts remaining)`);
        await this.delay(API_CONFIG.retryDelay);
        return this.retryRequest(requestFn, retries - 1);
      }
      throw error;
    }
  }

  private isRetryableError(error: ApiError): boolean {
    // Retry on network errors or 5xx errors
    return error.status >= 500 || error.status === 0;
  }

  private delay(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  // Config API
  async getConfig(): Promise<ConfigResponse> {
    return this.retryRequest(async () => {
      const response = await this.client.get<ConfigResponse>('/config');
      return response.data;
    });
  }

  async updateConfig(config: ConfigResponse): Promise<ApiResponse<ConfigResponse>> {
    const response = await this.client.post<ApiResponse<ConfigResponse>>('/config', config);
    return response.data;
  }

  async testNotification(): Promise<ApiResponse> {
    const response = await this.client.post<ApiResponse>('/test-notification');
    return response.data;
  }

  // Calibration API
  async startCalibration(): Promise<ApiResponse> {
    const response = await this.client.post<ApiResponse>('/calibration/start');
    return response.data;
  }

  async applyCalibration(): Promise<ApiResponse> {
    const response = await this.client.post<ApiResponse>('/calibration/apply');
    return response.data;
  }

  async getCalibrationStatus(): Promise<CalibrationStatus> {
    return this.retryRequest(async () => {
      const response = await this.client.get<CalibrationStatus>('/calibration/status');
      return response.data;
    });
  }

  // Health check
  async healthCheck(): Promise<{ status: string }> {
    return this.retryRequest(async () => {
      const response = await this.client.get<{ status: string }>('/health');
      return response.data;
    });
  }
}

// Export singleton instance
export const apiClient = new ApiClient();
