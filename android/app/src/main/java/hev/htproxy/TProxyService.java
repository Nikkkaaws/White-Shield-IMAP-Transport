package hev.htproxy;

public final class TProxyService {
    static {
        System.loadLibrary("hev-socks5-tunnel");
    }

    public native boolean TProxyStartService(String configPath, int tunFd);
    public native boolean TProxyStopService();
    public native boolean TProxyIsRunning();
    public native long[] TProxyGetStats();
}
