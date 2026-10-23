package test.logan.dianping.com.logan;

import java.util.UUID;

/** Main-thread submission state. A UI timeout does not cancel the SDK transport. */
final class WorkbenchSubmission {
    static final long TIMEOUT_MILLIS = 30000;
    static final class Attempt {
        final String id = UUID.randomUUID().toString();
        final String marker, fileDate;
        final long deadline;
        Attempt(String marker, String fileDate, long startedAt) {
            this.marker=marker;this.fileDate=fileDate;deadline=startedAt+TIMEOUT_MILLIS;
        }
    }
    private Attempt current;
    private boolean busy, completed;
    Attempt begin(String marker,String fileDate,long now) {
        current=new Attempt(marker,fileDate,now);busy=true;completed=false;return current;
    }
    void invalidate(){current=null;busy=false;completed=false;}
    boolean busy(){return busy;}
    boolean timeout(Attempt attempt,long now) {
        if(current!=attempt || !busy || completed || now<attempt.deadline)return false;
        busy=false;return true;
    }
    boolean complete(Attempt attempt) {
        if(current!=attempt || completed)return false;
        busy=false;completed=true;return true;
    }
}
